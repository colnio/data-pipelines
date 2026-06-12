package notify_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/colnio/data-pipelines/internal/config"
	"github.com/colnio/data-pipelines/internal/domain"
	"github.com/colnio/data-pipelines/internal/jobs"
	"github.com/colnio/data-pipelines/internal/notify"
	"github.com/colnio/data-pipelines/internal/testsupport"
)

// fakeTelegramServer returns an httptest.Server that mimics the Telegram Bot
// API. messagesReceived counts sendMessage calls; photosReceived counts
// sendPhoto calls. If forceError is true all requests return HTTP 500.
func fakeTelegramServer(t *testing.T, forceError bool) (srv *httptest.Server, messagesReceived, photosReceived *int) {
	t.Helper()
	msgs := 0
	photos := 0
	messagesReceived = &msgs
	photosReceived = &photos

	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if forceError {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"ok":false,"description":"internal error"}`))
			return
		}
		switch {
		case filepath.Base(r.URL.Path) == "sendMessage":
			msgs++
		case filepath.Base(r.URL.Path) == "sendPhoto":
			photos++
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	return srv, messagesReceived, photosReceived
}

// ── Test: happy path — message + photo sent, status=sent ─────────────────────

func TestHandleSend_Success(t *testing.T) {
	pool := testsupport.NewPool(t)
	testsupport.Truncate(t, pool, "notifications", "jobs", "notification_config")

	srv, msgs, photos := fakeTelegramServer(t, false)

	// Upsert a notification_config row with credentials.
	_, err := pool.Exec(context.Background(), `
		INSERT INTO notification_config
		    (bot_token, chat_id, on_awaiting_review, on_quarantined, on_published, on_test)
		VALUES ('testtoken', '-100testchat', true, true, true, true)
		ON CONFLICT ((true)) DO UPDATE
		    SET bot_token = 'testtoken', chat_id = '-100testchat'`)
	require.NoError(t, err)

	cfg := &config.Config{TelegramBotToken: "fallback-token"}
	q := jobs.NewQueue(pool)
	svc := notify.NewServiceWithBase(pool, cfg, q, nil, srv.URL)

	// Create a temp photo file.
	photoDir := t.TempDir()
	photoPath := filepath.Join(photoDir, "plot.png")
	require.NoError(t, os.WriteFile(photoPath, []byte("FAKE PNG"), 0o644))

	// Insert a notifications row manually and build a payload.
	var notifID int64
	require.NoError(t, pool.QueryRow(context.Background(), `
		INSERT INTO notifications (event_type, channel, target, payload_json, status)
		VALUES ('published', 'telegram', '', '{}', 'pending') RETURNING id`).Scan(&notifID))

	payload, _ := json.Marshal(map[string]any{
		"notification_id": notifID,
		"event_type":      "published",
		"run_id":          "run-abc",
		"message":         "hello from test",
		"photo_paths":     []string{photoPath},
	})

	job := domain.Job{
		ID:      1,
		JobType: domain.JobSendNotification,
		Payload: json.RawMessage(payload),
	}

	handlers := svc.Handlers()
	handler := handlers[domain.JobSendNotification]
	require.NotNil(t, handler)

	_, err = handler(context.Background(), job)
	require.NoError(t, err)

	assert.Equal(t, 1, *msgs, "expected 1 sendMessage call")
	assert.Equal(t, 1, *photos, "expected 1 sendPhoto call")

	// Verify DB status.
	var status string
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT status FROM notifications WHERE id = $1`, notifID).Scan(&status))
	assert.Equal(t, "sent", status)
}

// ── Test: event flag off → skipped, no HTTP call ─────────────────────────────

func TestHandleSend_Skipped_EventFlagOff(t *testing.T) {
	pool := testsupport.NewPool(t)
	testsupport.Truncate(t, pool, "notifications", "jobs", "notification_config")

	srv, msgs, photos := fakeTelegramServer(t, false)

	// Config with on_published=false.
	_, err := pool.Exec(context.Background(), `
		INSERT INTO notification_config
		    (bot_token, chat_id, on_awaiting_review, on_quarantined, on_published, on_test)
		VALUES ('tok', '-100chat', true, true, false, true)
		ON CONFLICT ((true)) DO UPDATE
		    SET bot_token = 'tok', chat_id = '-100chat', on_published = false`)
	require.NoError(t, err)

	cfg := &config.Config{}
	q := jobs.NewQueue(pool)
	svc := notify.NewServiceWithBase(pool, cfg, q, nil, srv.URL)

	var notifID int64
	require.NoError(t, pool.QueryRow(context.Background(), `
		INSERT INTO notifications (event_type, channel, target, payload_json, status)
		VALUES ('published', 'telegram', '', '{}', 'pending') RETURNING id`).Scan(&notifID))

	payload, _ := json.Marshal(map[string]any{
		"notification_id": notifID,
		"event_type":      "published",
		"run_id":          "run-xyz",
		"message":         "should be skipped",
		"photo_paths":     []string{},
	})

	job := domain.Job{ID: 2, JobType: domain.JobSendNotification, Payload: json.RawMessage(payload)}
	handlers := svc.Handlers()
	_, err = handlers[domain.JobSendNotification](context.Background(), job)
	require.NoError(t, err)

	assert.Equal(t, 0, *msgs, "no HTTP calls expected when flag is off")
	assert.Equal(t, 0, *photos)

	var status string
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT status FROM notifications WHERE id = $1`, notifID).Scan(&status))
	assert.Equal(t, "skipped", status)
}

// ── Test: server returns 500 → status=failed, error returned ─────────────────

func TestHandleSend_Failed_ServerError(t *testing.T) {
	pool := testsupport.NewPool(t)
	testsupport.Truncate(t, pool, "notifications", "jobs", "notification_config")

	srv, _, _ := fakeTelegramServer(t, true /* forceError */)

	_, err := pool.Exec(context.Background(), `
		INSERT INTO notification_config
		    (bot_token, chat_id, on_awaiting_review, on_quarantined, on_published, on_test)
		VALUES ('tok', '-100chat', true, true, true, true)
		ON CONFLICT ((true)) DO UPDATE
		    SET bot_token = 'tok', chat_id = '-100chat'`)
	require.NoError(t, err)

	cfg := &config.Config{}
	q := jobs.NewQueue(pool)
	svc := notify.NewServiceWithBase(pool, cfg, q, nil, srv.URL)

	var notifID int64
	require.NoError(t, pool.QueryRow(context.Background(), `
		INSERT INTO notifications (event_type, channel, target, payload_json, status)
		VALUES ('published', 'telegram', '', '{}', 'pending') RETURNING id`).Scan(&notifID))

	payload, _ := json.Marshal(map[string]any{
		"notification_id": notifID,
		"event_type":      "published",
		"run_id":          "run-fail",
		"message":         "will fail",
		"photo_paths":     []string{},
	})

	job := domain.Job{ID: 3, JobType: domain.JobSendNotification, Payload: json.RawMessage(payload)}
	handlers := svc.Handlers()
	_, handlerErr := handlers[domain.JobSendNotification](context.Background(), job)
	require.Error(t, handlerErr, "handler must return an error so the queue retries")

	var status string
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT status FROM notifications WHERE id = $1`, notifID).Scan(&status))
	assert.Equal(t, "failed", status)
}

// ── Test: config GET/PUT round-trip ──────────────────────────────────────────

func TestConfigRoundTrip(t *testing.T) {
	pool := testsupport.NewPool(t)
	testsupport.Truncate(t, pool, "notification_config")

	ctx := context.Background()

	// UPSERT via direct SQL (mirrors what handleConfigPut does).
	var botToken, chatID string
	var onPub bool
	err := pool.QueryRow(ctx, `
		INSERT INTO notification_config
		    (bot_token, chat_id, on_awaiting_review, on_quarantined, on_published, on_test)
		VALUES ('mytoken', '-100foo', true, true, false, true)
		ON CONFLICT ((true)) DO UPDATE
		    SET bot_token = 'mytoken', chat_id = '-100foo', on_published = false, updated_at = now()
		RETURNING bot_token, chat_id, on_published`).Scan(&botToken, &chatID, &onPub)
	require.NoError(t, err)
	assert.Equal(t, "mytoken", botToken)
	assert.Equal(t, "-100foo", chatID)
	assert.False(t, onPub)

	// Upsert again with on_published=true.
	err = pool.QueryRow(ctx, `
		INSERT INTO notification_config
		    (bot_token, chat_id, on_awaiting_review, on_quarantined, on_published, on_test)
		VALUES ('mytoken', '-100foo', true, true, true, true)
		ON CONFLICT ((true)) DO UPDATE
		    SET on_published = true, updated_at = now()
		RETURNING on_published`).Scan(&onPub)
	require.NoError(t, err)
	assert.True(t, onPub)
}
