package ingest

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/colnio/data-pipelines/internal/domain"
)

// HTTPTransport pulls archives from measurement-PC agents over authenticated
// LAN/VPN HTTP (architecture §13). The agent base URL is resolved per agent so
// measurement PCs are never addressed through the public proxy (§4).
type HTTPTransport struct {
	client    *http.Client
	baseURLs  map[string]string // agent_id → base URL, e.g. http://measpc-01.lan:9101
	serverKey string            // shared key the server presents to the agent (optional)
	maxBytes  int64
}

// HTTPTransportConfig configures the HTTP transport.
type HTTPTransportConfig struct {
	BaseURLs  map[string]string
	ServerKey string
	MaxBytes  int64
	Timeout   time.Duration
}

// NewHTTPTransport builds an HTTP-pull transport.
func NewHTTPTransport(cfg HTTPTransportConfig) *HTTPTransport {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	return &HTTPTransport{
		client:    &http.Client{Timeout: timeout},
		baseURLs:  cfg.BaseURLs,
		serverKey: cfg.ServerKey,
		maxBytes:  cfg.MaxBytes,
	}
}

// ParseBaseURLs parses an "agentid=url,agentid2=url2" spec into a map.
func ParseBaseURLs(spec string) map[string]string {
	out := map[string]string{}
	for _, pair := range strings.Split(spec, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		eq := strings.IndexByte(pair, '=')
		if eq <= 0 {
			continue
		}
		out[strings.TrimSpace(pair[:eq])] = strings.TrimSpace(pair[eq+1:])
	}
	return out
}

// FetchArchive streams the run's archive from its agent to destPath.
func (t *HTTPTransport) FetchArchive(ctx context.Context, agentID string, r domain.Run, archiveName, destPath string) error {
	base, ok := t.baseURLs[agentID]
	if !ok || base == "" {
		return fmt.Errorf("no base URL configured for agent %q", agentID)
	}
	u, err := url.Parse(strings.TrimRight(base, "/") + "/v1/runs/" + url.PathEscape(r.ID) + "/archive")
	if err != nil {
		return fmt.Errorf("build agent url: %w", err)
	}
	q := u.Query()
	q.Set("archive", archiveName)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	if t.serverKey != "" {
		req.Header.Set("X-Server-Key", t.serverKey)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("pull archive: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("agent returned %s pulling archive", resp.Status)
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	tmp := destPath + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	var reader io.Reader = resp.Body
	if t.maxBytes > 0 {
		reader = io.LimitReader(resp.Body, t.maxBytes+1)
	}
	n, copyErr := io.Copy(f, reader)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write archive: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if t.maxBytes > 0 && n > t.maxBytes {
		_ = os.Remove(tmp)
		return fmt.Errorf("archive exceeds max bytes %d", t.maxBytes)
	}
	return os.Rename(tmp, destPath)
}
