// Package jupyter provides the JupyterHub connection service.
// It exposes two endpoints:
//   - GET  /v1/jupyter/info       — reachability probe (never errors)
//   - POST /v1/jupyter/connection — mint a per-user Hub API token
//
// Both require platform.ScopeReadCatalog.
package jupyter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/colnio/data-pipelines/internal/config"
)

var usernameUnsafe = regexp.MustCompile(`[^a-z0-9_-]`)

// NormalizeUsername derives a valid Linux/PAM username from an email address,
// replicating the Python logic in deploy/jupyterhub/labdata_authenticator.py:
//  1. lowercase the whole address
//  2. take the local part (before '@')
//  3. replace any character not in [a-z0-9_-] with '_'
func NormalizeUsername(email string) string {
	lower := strings.ToLower(email)
	local := strings.SplitN(lower, "@", 2)[0]
	return usernameUnsafe.ReplaceAllString(local, "_")
}

// normalizeUsername is the internal alias used by service methods.
func normalizeUsername(email string) string { return NormalizeUsername(email) }

// Service holds the runtime dependencies for JupyterHub integration.
type Service struct {
	cfg    *config.Config
	log    *slog.Logger
	client *http.Client
}

// NewService constructs a Service. The HTTP client has a conservative timeout
// so Hub calls cannot stall a handler indefinitely.
func NewService(cfg *config.Config, logger *slog.Logger) *Service {
	return &Service{
		cfg: cfg,
		log: logger,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// hubReachable probes <hubURL>/hub/api with a short timeout.
// It returns false on any error or non-2xx response; it never returns an error
// itself so callers can always surface a "reachable: false" result.
func (s *Service) hubReachable(ctx context.Context) bool {
	if s.cfg.JupyterHubURL == "" {
		return false
	}
	url := strings.TrimRight(s.cfg.JupyterHubURL, "/") + "/hub/api"

	// Use a short independent timeout — the probe must not block for the full
	// client timeout when the Hub is down.
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, url, nil)
	if err != nil {
		s.log.Debug("jupyter: could not build probe request", "err", err)
		return false
	}
	resp, err := s.client.Do(req)
	if err != nil {
		s.log.Debug("jupyter: hub unreachable", "url", url, "err", err)
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

// hubTokenResponse is the JSON body returned by the Hub token-mint endpoint.
type hubTokenResponse struct {
	Token string `json:"token"`
}

// ensureUser idempotently creates the Hub user so a token can be minted even
// before the user's first browser login to JupyterHub. 201 (created), 409
// (already exists) and any other 2xx all count as success.
func (s *Service) ensureUser(ctx context.Context, username string) error {
	hubURL := strings.TrimRight(s.cfg.JupyterHubURL, "/")
	endpoint := fmt.Sprintf("%s/hub/api/users/%s", hubURL, username)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return fmt.Errorf("jupyter: build create-user request: %w", err)
	}
	req.Header.Set("Authorization", "token "+s.cfg.JupyterHubAdminToken)

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("jupyter: create-user request failed: %w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusConflict || (resp.StatusCode >= 200 && resp.StatusCode < 300) {
		return nil
	}
	return fmt.Errorf("hub create-user HTTP %d: %s", resp.StatusCode, string(b))
}

// mintToken mints a per-user JupyterHub API token via the Hub admin API. It
// first ensures the user exists, then creates a short-lived token. Returns the
// raw token string.
func (s *Service) mintToken(ctx context.Context, username string) (string, error) {
	if err := s.ensureUser(ctx, username); err != nil {
		return "", err
	}

	hubURL := strings.TrimRight(s.cfg.JupyterHubURL, "/")
	endpoint := fmt.Sprintf("%s/hub/api/users/%s/tokens", hubURL, username)

	payload := map[string]any{
		"expires_in": 86400,
		"note":       "vscode-connection",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("jupyter: marshal token request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("jupyter: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "token "+s.cfg.JupyterHubAdminToken)

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("jupyter: hub request failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("hub returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var tr hubTokenResponse
	if err := json.Unmarshal(respBody, &tr); err != nil {
		return "", fmt.Errorf("jupyter: parse hub token response: %w", err)
	}
	if tr.Token == "" {
		return "", fmt.Errorf("jupyter: hub returned empty token")
	}
	return tr.Token, nil
}

// ensureServer starts the user's single-user server (if not already running) so
// the generated connection link is immediately usable by an IDE — otherwise the
// IDE hits /user/<name>/api/... with no server behind it. It POSTs the Hub
// server-spawn endpoint (idempotent: 201 started, 202 pending, 400 already
// running) then waits, bounded, for the server to report ready. It is
// best-effort: a slow spawn is not treated as fatal, since the server usually
// finishes coming up moments later and the user can also launch it in-browser.
func (s *Service) ensureServer(ctx context.Context, username string) error {
	hubURL := strings.TrimRight(s.cfg.JupyterHubURL, "/")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hubURL+"/hub/api/users/"+username+"/server", nil)
	if err != nil {
		return fmt.Errorf("jupyter: build spawn request: %w", err)
	}
	req.Header.Set("Authorization", "token "+s.cfg.JupyterHubAdminToken)

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("jupyter: spawn request failed: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	// 400 == already running (or pending); treat as success.
	if !(resp.StatusCode == http.StatusBadRequest || (resp.StatusCode >= 200 && resp.StatusCode < 300)) {
		return fmt.Errorf("hub spawn HTTP %d: %s", resp.StatusCode, string(body))
	}

	// Poll readiness for a bounded time; a timeout is non-fatal.
	waitCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	for {
		if s.serverReady(waitCtx, hubURL, username) {
			return nil
		}
		select {
		case <-waitCtx.Done():
			return nil
		case <-time.After(time.Second):
		}
	}
}

// serverReady reports whether the user's default single-user server is ready.
func (s *Service) serverReady(ctx context.Context, hubURL, username string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, hubURL+"/hub/api/users/"+username, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "token "+s.cfg.JupyterHubAdminToken)
	resp, err := s.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var u struct {
		Servers map[string]struct {
			Ready bool `json:"ready"`
		} `json:"servers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return false
	}
	srv, ok := u.Servers[""]
	return ok && srv.Ready
}
