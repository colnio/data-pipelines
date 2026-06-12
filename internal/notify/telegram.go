package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// telegramHTTPClient is the shared HTTP client with a 10-second timeout.
var telegramHTTPClient = &http.Client{Timeout: 10 * time.Second}

// telegramResponse is the minimal shape of a Telegram Bot API response.
type telegramResponse struct {
	OK          bool            `json:"ok"`
	Description string          `json:"description,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
}

// sendMessage sends a text message to a Telegram chat via sendMessage.
// parse_mode is set to "HTML" so callers can use basic markup.
func sendMessage(ctx context.Context, base, token, chatID, text string) error {
	url := fmt.Sprintf("%s/bot%s/sendMessage", base, token)

	body, err := json.Marshal(map[string]string{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "HTML",
	})
	if err != nil {
		return fmt.Errorf("telegram: marshal sendMessage body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram: build sendMessage request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := telegramHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("telegram: sendMessage HTTP: %w", err)
	}
	defer resp.Body.Close()

	return checkTelegramResponse(resp)
}

// sendPhoto sends a photo file to a Telegram chat via sendPhoto.
// photoPath is a local filesystem path. caption is optional.
func sendPhoto(ctx context.Context, base, token, chatID, photoPath, caption string) error {
	url := fmt.Sprintf("%s/bot%s/sendPhoto", base, token)

	f, err := os.Open(photoPath)
	if err != nil {
		return fmt.Errorf("telegram: open photo %q: %w", photoPath, err)
	}
	defer f.Close()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	// chat_id field.
	if err := mw.WriteField("chat_id", chatID); err != nil {
		return fmt.Errorf("telegram: write chat_id field: %w", err)
	}

	// caption field (may be empty).
	if caption != "" {
		if err := mw.WriteField("caption", caption); err != nil {
			return fmt.Errorf("telegram: write caption field: %w", err)
		}
	}

	// photo file field.
	part, err := mw.CreateFormFile("photo", filepath.Base(photoPath))
	if err != nil {
		return fmt.Errorf("telegram: create form file: %w", err)
	}
	if _, err := io.Copy(part, f); err != nil {
		return fmt.Errorf("telegram: copy photo: %w", err)
	}
	if err := mw.Close(); err != nil {
		return fmt.Errorf("telegram: close multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return fmt.Errorf("telegram: build sendPhoto request: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := telegramHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("telegram: sendPhoto HTTP: %w", err)
	}
	defer resp.Body.Close()

	return checkTelegramResponse(resp)
}

// checkTelegramResponse reads the Telegram API response and returns an error
// if the API reported a failure or the HTTP status was not 2xx.
func checkTelegramResponse(resp *http.Response) error {
	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("telegram: read response body: %w", err)
	}

	var tResp telegramResponse
	if jsonErr := json.Unmarshal(rawBody, &tResp); jsonErr != nil {
		// Not valid JSON — surface the HTTP status.
		return fmt.Errorf("telegram: unexpected response (status %d): %s", resp.StatusCode, rawBody)
	}

	if !tResp.OK {
		return fmt.Errorf("telegram: API error: %s", tResp.Description)
	}
	return nil
}
