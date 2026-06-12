// Package notify provides Telegram notification delivery via the durable jobs
// queue (architecture §21). It is the sole writer of the notifications table;
// other packages call Enqueue to emit events without importing this package's
// internals.
package notify

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/colnio/data-pipelines/internal/domain"
	"github.com/colnio/data-pipelines/internal/jobs"
)

// notifyPayload is the JSON shape stored in jobs.payload_json for
// send_notification jobs. This exact shape must match what the Python pipeline
// enqueues so that both Go and Python callers can trigger the same handler.
type notifyPayload struct {
	NotificationID int64    `json:"notification_id"`
	EventType      string   `json:"event_type"`
	RunID          string   `json:"run_id"`
	Message        string   `json:"message"`
	PhotoPaths     []string `json:"photo_paths"`
}

// Enqueue inserts a notifications row and enqueues a send_notification job.
// It is best-effort: callers should ignore the error (assign to _) so a
// notification failure never aborts the primary action.
//
// Parameters:
//   - event: one of "awaiting_review", "quarantined", "published", "test".
//   - runID: the run_id string (empty string for test events).
//   - message: human-readable text to send.
//   - photoPaths: optional local file paths to attach as Telegram photos.
func Enqueue(ctx context.Context, q *jobs.Queue, pool *pgxpool.Pool, event, runID, message string, photoPaths []string) error {
	// Build the payload_json that will be stored in the notifications row.
	initialPayload, err := json.Marshal(map[string]any{
		"event_type":  event,
		"run_id":      runID,
		"message":     message,
		"photo_paths": photoPaths,
	})
	if err != nil {
		return fmt.Errorf("notify: marshal initial payload: %w", err)
	}

	// INSERT a notifications row to track delivery lifecycle.
	var notificationID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO notifications (event_type, channel, target, payload_json, status)
		VALUES ($1, 'telegram', '', $2, 'pending')
		RETURNING id`,
		event, initialPayload,
	).Scan(&notificationID)
	if err != nil {
		return fmt.Errorf("notify: insert notifications row: %w", err)
	}

	// Build the full job payload including the notification_id so the handler
	// can update the row on completion.
	if photoPaths == nil {
		photoPaths = []string{}
	}
	p := notifyPayload{
		NotificationID: notificationID,
		EventType:      event,
		RunID:          runID,
		Message:        message,
		PhotoPaths:     photoPaths,
	}
	payload, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("notify: marshal job payload: %w", err)
	}

	// Use runID in the idempotency key; for test events with no run, just use event+id.
	idemKey := fmt.Sprintf("notify:%s:%s:%d", event, runID, notificationID)

	var runIDPtr *string
	if runID != "" {
		cp := runID
		runIDPtr = &cp
	}

	_, err = q.Enqueue(ctx, jobs.EnqueueParams{
		JobType:        domain.JobSendNotification,
		RunID:          runIDPtr,
		Priority:       3,
		IdempotencyKey: idemKey,
		Payload:        json.RawMessage(payload),
	})
	if err != nil {
		return fmt.Errorf("notify: enqueue job: %w", err)
	}
	return nil
}
