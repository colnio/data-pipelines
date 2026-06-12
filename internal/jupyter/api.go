package jupyter

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/colnio/data-pipelines/internal/platform"
)

// Register wires the Jupyter endpoints onto the huma API.
func Register(api huma.API, svc *Service) {
	huma.Register(api, huma.Operation{
		OperationID: "jupyter-info",
		Method:      http.MethodGet,
		Path:        "/v1/jupyter/info",
		Summary:     "JupyterHub info",
		Description: "Returns the Hub URL and a reachability probe result. Never errors — reachable is false when the Hub is down.",
		Tags:        []string{"jupyter"},
	}, svc.handleInfo)

	huma.Register(api, huma.Operation{
		OperationID: "jupyter-connection",
		Method:      http.MethodPost,
		Path:        "/v1/jupyter/connection",
		Summary:     "Get JupyterHub connection",
		Description: "Mints a 24-hour Hub token for the calling user and returns the full connection info needed by VS Code.",
		Tags:        []string{"jupyter"},
	}, svc.handleConnection)
}

// ── GET /v1/jupyter/info ──────────────────────────────────────────────────────

// jupyterInfoInput has no query/path params — the context carries the principal.
type jupyterInfoInput struct{}

type jupyterInfoBody struct {
	HubURL    string    `json:"hub_url"`
	Reachable bool      `json:"reachable"`
	CheckedAt time.Time `json:"checked_at"`
}

type jupyterInfoOutput struct {
	Body jupyterInfoBody
}

func (s *Service) handleInfo(ctx context.Context, _ *jupyterInfoInput) (*jupyterInfoOutput, error) {
	p, ok := platform.PrincipalFrom(ctx)
	if !ok {
		return nil, platform.Unauthorized("not authenticated")
	}
	if err := platform.RequireScope(p, platform.ScopeReadCatalog); err != nil {
		return nil, err
	}

	reachable := s.hubReachable(ctx)
	return &jupyterInfoOutput{
		Body: jupyterInfoBody{
			HubURL:    s.cfg.JupyterHubURL,
			Reachable: reachable,
			CheckedAt: time.Now().UTC(),
		},
	}, nil
}

// ── POST /v1/jupyter/connection ───────────────────────────────────────────────

// jupyterConnectionInput carries no body or path params — user identity comes
// from the principal on the context.
type jupyterConnectionInput struct{}

type jupyterConnectionBody struct {
	ServerURL       string    `json:"server_url"`
	Token           string    `json:"token"`
	VSCodeServerURI string    `json:"vscode_server_uri"`
	HubURL          string    `json:"hub_url"`
	Username        string    `json:"username"`
	ExpiresAt       time.Time `json:"expires_at"`
}

type jupyterConnectionOutput struct {
	Body jupyterConnectionBody
}

func (s *Service) handleConnection(ctx context.Context, _ *jupyterConnectionInput) (*jupyterConnectionOutput, error) {
	p, ok := platform.PrincipalFrom(ctx)
	if !ok {
		return nil, platform.Unauthorized("not authenticated")
	}
	if err := platform.RequireScope(p, platform.ScopeReadCatalog); err != nil {
		return nil, err
	}

	if s.cfg.JupyterHubURL == "" || s.cfg.JupyterHubAdminToken == "" {
		return nil, platform.Errorf(http.StatusServiceUnavailable, "jupyter.not_configured", "JupyterHub is not configured")
	}

	username := normalizeUsername(p.Email)
	token, err := s.mintToken(ctx, username)
	if err != nil {
		s.log.Error("jupyter: failed to mint token", "username", username, "err", err)
		return nil, platform.Errorf(http.StatusBadGateway, "jupyter.hub_error", "failed to obtain JupyterHub token: "+err.Error())
	}

	// Best-effort: start the user's single-user server so the link works
	// immediately. A failure here doesn't block returning the link — the user
	// can also launch it via "Open in browser".
	if err := s.ensureServer(ctx, username); err != nil {
		s.log.Warn("jupyter: could not ensure single-user server", "username", username, "err", err)
	}

	hubURL := strings.TrimRight(s.cfg.JupyterHubURL, "/")
	serverURL := hubURL + "/user/" + username + "/"
	vsCodeURI := serverURL + "?token=" + token
	expiresAt := time.Now().UTC().Add(24 * time.Hour)

	return &jupyterConnectionOutput{
		Body: jupyterConnectionBody{
			ServerURL:       serverURL,
			Token:           token,
			VSCodeServerURI: vsCodeURI,
			HubURL:          hubURL,
			Username:        username,
			ExpiresAt:       expiresAt,
		},
	}, nil
}
