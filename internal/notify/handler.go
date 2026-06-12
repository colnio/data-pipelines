package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/colnio/data-pipelines/internal/domain"
	"github.com/colnio/data-pipelines/internal/jobs"
)

// notificationConfig holds a loaded row from the notification_config table.
type notificationConfig struct {
	BotToken          string
	ChatID            string
	OnAwaitingReview  bool
	OnQuarantined     bool
	OnPublished       bool
	OnTest            bool
}

// defaultConfig returns an all-enabled config with empty credentials (used when
// no row exists in notification_config). An empty token/chat_id causes the
// handler to skip delivery, which is the correct safe default.
func defaultConfig() notificationConfig {
	return notificationConfig{
		OnAwaitingReview: true,
		OnQuarantined:    true,
		OnPublished:      true,
		OnTest:           true,
	}
}

// Handlers returns the map of job handlers owned by this module. The
// orchestrator merges this into the worker's handler map.
func (s *Service) Handlers() map[domain.JobType]jobs.Handler {
	return map[domain.JobType]jobs.Handler{
		domain.JobSendNotification: s.handleSend,
	}
}

// handleSend is the worker handler for send_notification jobs.
//
// Flow:
//  1. Unmarshal the job payload.
//  2. Load notification_config (single row; defaults if none).
//  3. Check the per-event flag; if off OR credentials empty → mark skipped, return nil.
//  4. Resolve bot token (config row > env fallback).
//  5. Send the text message, then each photo.
//  6. On success: mark sent. On failure: mark failed and return the error (queue retries).
func (s *Service) handleSend(ctx context.Context, job domain.Job) (json.RawMessage, error) {
	// 1. Parse payload.
	var p notifyPayload
	if err := json.Unmarshal(job.Payload, &p); err != nil {
		return nil, fmt.Errorf("notify: unmarshal payload for job %d: %w", job.ID, err)
	}

	// 2. Load notification_config.
	cfg, err := s.loadConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("notify: load config: %w", err)
	}

	// 3. Check the per-event flag.
	enabled := s.eventEnabled(cfg, p.EventType)
	token := cfg.BotToken
	if token == "" {
		token = s.cfg.TelegramBotToken
	}

	if !enabled || token == "" || cfg.ChatID == "" {
		// Mark skipped — not a retryable error.
		_ = s.setStatus(ctx, p.NotificationID, "skipped", "")
		return json.RawMessage(`{}`), nil
	}

	// 5. Send text message.
	sendErr := sendMessage(ctx, s.telegramBaseURL, token, cfg.ChatID, p.Message)
	if sendErr == nil {
		// Send photos.
		for _, path := range p.PhotoPaths {
			if path == "" {
				continue
			}
			if photoErr := sendPhoto(ctx, s.telegramBaseURL, token, cfg.ChatID, path, ""); photoErr != nil {
				sendErr = photoErr
				break
			}
		}
	}

	// 6. Update notifications row.
	if sendErr != nil {
		_ = s.setStatus(ctx, p.NotificationID, "failed", sendErr.Error())
		return nil, sendErr // return error so queue retries
	}

	_ = s.setStatusSent(ctx, p.NotificationID)
	return json.RawMessage(`{}`), nil
}

// loadConfig reads the singleton notification_config row. If no row exists it
// returns the safe all-enabled default with empty credentials.
func (s *Service) loadConfig(ctx context.Context) (notificationConfig, error) {
	var cfg notificationConfig
	err := s.pool.QueryRow(ctx, `
		SELECT bot_token, chat_id,
		       on_awaiting_review, on_quarantined, on_published, on_test
		FROM notification_config
		LIMIT 1`,
	).Scan(
		&cfg.BotToken, &cfg.ChatID,
		&cfg.OnAwaitingReview, &cfg.OnQuarantined, &cfg.OnPublished, &cfg.OnTest,
	)
	if err != nil {
		// pgx returns pgx.ErrNoRows when the table is empty; treat as defaults.
		return defaultConfig(), nil //nolint:nilerr
	}
	return cfg, nil
}

// eventEnabled checks whether the given event type has its flag set.
func (s *Service) eventEnabled(cfg notificationConfig, event string) bool {
	switch event {
	case "awaiting_review":
		return cfg.OnAwaitingReview
	case "quarantined":
		return cfg.OnQuarantined
	case "published":
		return cfg.OnPublished
	case "test":
		return cfg.OnTest
	default:
		return true // unknown event types are allowed through by default
	}
}

// setStatus updates the status and last_error of a notifications row.
func (s *Service) setStatus(ctx context.Context, id int64, status, lastErr string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE notifications SET status = $1, last_error = $2 WHERE id = $3`,
		status, lastErr, id)
	return err
}

// setStatusSent marks a notifications row as sent and records the sent_at timestamp.
func (s *Service) setStatusSent(ctx context.Context, id int64) error {
	now := time.Now()
	_, err := s.pool.Exec(ctx,
		`UPDATE notifications SET status = 'sent', sent_at = $1 WHERE id = $2`,
		now, id)
	return err
}
