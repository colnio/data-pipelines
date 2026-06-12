package notify

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/colnio/data-pipelines/internal/platform"
)

// ── Config GET/PUT types ──────────────────────────────────────────────────────

type notifyConfigBody struct {
	BotToken         string `json:"bot_token" doc:"Telegram bot token; empty means use the server env var"`
	ChatID           string `json:"chat_id" doc:"Telegram chat or channel ID"`
	OnAwaitingReview bool   `json:"on_awaiting_review" doc:"Notify when a run enters awaiting_review"`
	OnQuarantined    bool   `json:"on_quarantined" doc:"Notify when a run is quarantined"`
	OnPublished      bool   `json:"on_published" doc:"Notify when a run is published"`
	OnTest           bool   `json:"on_test" doc:"Send test notifications"`
	UpdatedAt        time.Time `json:"updated_at,omitempty"`
	CreatedAt        time.Time `json:"created_at,omitempty"`
}

type notifyConfigGetInput struct{}

type notifyConfigGetOutput struct {
	Body notifyConfigBody
}

func (s *Service) handleConfigGet(ctx context.Context, _ *notifyConfigGetInput) (*notifyConfigGetOutput, error) {
	p, ok := platform.PrincipalFrom(ctx)
	if !ok {
		return nil, platform.Unauthorized("not authenticated")
	}
	if err := platform.RequirePrivileged(p); err != nil {
		return nil, err
	}

	var body notifyConfigBody
	err := s.pool.QueryRow(ctx, `
		SELECT bot_token, chat_id,
		       on_awaiting_review, on_quarantined, on_published, on_test,
		       updated_at, created_at
		FROM notification_config LIMIT 1`,
	).Scan(
		&body.BotToken, &body.ChatID,
		&body.OnAwaitingReview, &body.OnQuarantined, &body.OnPublished, &body.OnTest,
		&body.UpdatedAt, &body.CreatedAt,
	)
	if err != nil {
		// No row yet — return all-default enabled config.
		body = notifyConfigBody{
			OnAwaitingReview: true,
			OnQuarantined:    true,
			OnPublished:      true,
			OnTest:           true,
		}
	}

	return &notifyConfigGetOutput{Body: body}, nil
}

// ── Config PUT ────────────────────────────────────────────────────────────────

type notifyConfigPutInput struct {
	Body struct {
		BotToken         string `json:"bot_token"`
		ChatID           string `json:"chat_id"`
		OnAwaitingReview bool   `json:"on_awaiting_review"`
		OnQuarantined    bool   `json:"on_quarantined"`
		OnPublished      bool   `json:"on_published"`
		OnTest           bool   `json:"on_test"`
	}
}

type notifyConfigPutOutput struct {
	Body notifyConfigBody
}

func (s *Service) handleConfigPut(ctx context.Context, in *notifyConfigPutInput) (*notifyConfigPutOutput, error) {
	p, ok := platform.PrincipalFrom(ctx)
	if !ok {
		return nil, platform.Unauthorized("not authenticated")
	}
	if err := platform.RequireAdmin(p); err != nil {
		return nil, err
	}

	var body notifyConfigBody
	err := s.pool.QueryRow(ctx, `
		INSERT INTO notification_config
		    (bot_token, chat_id, on_awaiting_review, on_quarantined, on_published, on_test, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, now())
		ON CONFLICT ((true)) DO UPDATE
		    SET bot_token          = EXCLUDED.bot_token,
		        chat_id            = EXCLUDED.chat_id,
		        on_awaiting_review = EXCLUDED.on_awaiting_review,
		        on_quarantined     = EXCLUDED.on_quarantined,
		        on_published       = EXCLUDED.on_published,
		        on_test            = EXCLUDED.on_test,
		        updated_at         = now()
		RETURNING bot_token, chat_id,
		          on_awaiting_review, on_quarantined, on_published, on_test,
		          updated_at, created_at`,
		in.Body.BotToken, in.Body.ChatID,
		in.Body.OnAwaitingReview, in.Body.OnQuarantined,
		in.Body.OnPublished, in.Body.OnTest,
	).Scan(
		&body.BotToken, &body.ChatID,
		&body.OnAwaitingReview, &body.OnQuarantined, &body.OnPublished, &body.OnTest,
		&body.UpdatedAt, &body.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	return &notifyConfigPutOutput{Body: body}, nil
}

// ── Test notification ─────────────────────────────────────────────────────────

type notifyTestInput struct{}

type notifyTestOutput struct {
	Body struct {
		Enqueued bool  `json:"enqueued"`
		JobID    int64 `json:"job_id,omitempty"`
	}
}

func (s *Service) handleTest(ctx context.Context, _ *notifyTestInput) (*notifyTestOutput, error) {
	p, ok := platform.PrincipalFrom(ctx)
	if !ok {
		return nil, platform.Unauthorized("not authenticated")
	}
	if err := platform.RequireAdmin(p); err != nil {
		return nil, err
	}

	if err := Enqueue(ctx, s.queue, s.pool, "test", "", "Test notification from lab-data", nil); err != nil {
		return nil, err
	}

	out := &notifyTestOutput{}
	out.Body.Enqueued = true
	return out, nil
}

// ── Notification log ──────────────────────────────────────────────────────────

type notificationRow struct {
	ID          int64      `json:"id"`
	EventType   string     `json:"event_type"`
	Channel     string     `json:"channel"`
	Target      string     `json:"target"`
	Status      string     `json:"status"`
	LastError   string     `json:"last_error"`
	CreatedAt   time.Time  `json:"created_at"`
	SentAt      *time.Time `json:"sent_at,omitempty"`
}

type notifyLogInput struct {
	Limit int `query:"limit"`
}

type notifyLogOutput struct {
	Body struct {
		Notifications []notificationRow `json:"notifications"`
	}
}

func (s *Service) handleLog(ctx context.Context, in *notifyLogInput) (*notifyLogOutput, error) {
	p, ok := platform.PrincipalFrom(ctx)
	if !ok {
		return nil, platform.Unauthorized("not authenticated")
	}
	if err := platform.RequirePrivileged(p); err != nil {
		return nil, err
	}

	limit := in.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, event_type, channel, target, status, last_error, created_at, sent_at
		FROM notifications
		ORDER BY created_at DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var notifications []notificationRow
	for rows.Next() {
		var n notificationRow
		if err := rows.Scan(
			&n.ID, &n.EventType, &n.Channel, &n.Target,
			&n.Status, &n.LastError, &n.CreatedAt, &n.SentAt,
		); err != nil {
			return nil, err
		}
		notifications = append(notifications, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := &notifyLogOutput{}
	out.Body.Notifications = notifications
	return out, nil
}

// Register wires all notify admin endpoints onto the huma API.
func Register(api huma.API, svc *Service) {
	huma.Register(api, huma.Operation{
		OperationID: "notify-config-get",
		Method:      http.MethodGet,
		Path:        "/v1/admin/notifications/config",
		Summary:     "Get notification configuration",
		Tags:        []string{"admin", "notifications"},
	}, svc.handleConfigGet)

	huma.Register(api, huma.Operation{
		OperationID: "notify-config-put",
		Method:      http.MethodPut,
		Path:        "/v1/admin/notifications/config",
		Summary:     "Update notification configuration",
		Tags:        []string{"admin", "notifications"},
	}, svc.handleConfigPut)

	huma.Register(api, huma.Operation{
		OperationID: "notify-test",
		Method:      http.MethodPost,
		Path:        "/v1/admin/notifications/test",
		Summary:     "Send a test notification",
		Tags:        []string{"admin", "notifications"},
	}, svc.handleTest)

	huma.Register(api, huma.Operation{
		OperationID: "notify-log",
		Method:      http.MethodGet,
		Path:        "/v1/admin/notifications/log",
		Summary:     "Recent notification delivery log",
		Tags:        []string{"admin", "notifications"},
	}, svc.handleLog)
}
