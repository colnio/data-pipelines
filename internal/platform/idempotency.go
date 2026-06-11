package platform

import (
	"bytes"
	"net/http"
)

// Idempotency replays the original response for a repeated write that carries
// the same Idempotency-Key (per principal). Keys live 24h (GC'd nightly by a
// River job). Applies to POST/PUT/PATCH/DELETE only; requests without the
// header pass through. Responses with status >= 500 are not stored so a
// transient failure can be retried.
func Idempotency(store IdempotencyStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if store == nil || !isWrite(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			key := r.Header.Get("Idempotency-Key")
			if key == "" {
				next.ServeHTTP(w, r)
				return
			}
			pkey := principalKey(r)
			if pkey == "" {
				next.ServeHTTP(w, r)
				return
			}
			ctx := r.Context()
			status, body, found, err := store.Lookup(ctx, pkey, key)
			if err != nil {
				writeError(w, Errorf(http.StatusServiceUnavailable, "idempotency.unavailable", "idempotency store unavailable"))
				return
			}
			if found {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Idempotency-Replayed", "true")
				w.WriteHeader(status)
				_, _ = w.Write(body)
				return
			}

			rec := &capturingWriter{ResponseWriter: w, status: http.StatusOK, buf: &bytes.Buffer{}}
			next.ServeHTTP(rec, r)

			if rec.status < 500 {
				_ = store.Save(ctx, pkey, key, rec.status, rec.buf.Bytes())
			}
		})
	}
}

func isWrite(m string) bool {
	switch m {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

func principalKey(r *http.Request) string {
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		return ""
	}
	if p.ViaTokenID != nil {
		return "tok:" + p.ViaTokenID.String()
	}
	return "usr:" + p.UserID.String()
}

// capturingWriter tees the response body and status so successful writes can be
// stored for replay.
type capturingWriter struct {
	http.ResponseWriter
	status      int
	buf         *bytes.Buffer
	wroteHeader bool
}

func (c *capturingWriter) WriteHeader(status int) {
	if c.wroteHeader {
		return
	}
	c.status = status
	c.wroteHeader = true
	c.ResponseWriter.WriteHeader(status)
}

func (c *capturingWriter) Write(b []byte) (int, error) {
	if !c.wroteHeader {
		c.WriteHeader(http.StatusOK)
	}
	c.buf.Write(b)
	return c.ResponseWriter.Write(b)
}
