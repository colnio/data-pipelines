package fileserve

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// sign returns a hex-encoded HMAC-SHA256 signature for a signed URL.
//
//	msg = scope + ":" + runID + ":" + id + ":" + exp
//
// scope is "artifact" or "file"; id is the artifact or file ID (as string);
// exp is a Unix timestamp (seconds).
func (s *Service) sign(scope, runID, id string, exp int64) string {
	mac := hmac.New(sha256.New, []byte(s.cfg.JWTSigningKey))
	_, _ = mac.Write([]byte(fmt.Sprintf("%s:%s:%s:%d", scope, runID, id, exp)))
	return hex.EncodeToString(mac.Sum(nil))
}

// verify returns true when the signature is valid AND has not expired.
// It uses hmac.Equal for constant-time comparison to prevent timing attacks.
func (s *Service) verify(scope, runID, id string, exp int64, sig string) bool {
	if time.Now().Unix() >= exp {
		return false
	}
	want := s.sign(scope, runID, id, exp)
	wantBytes, err := hex.DecodeString(want)
	if err != nil {
		return false
	}
	gotBytes, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	return hmac.Equal(wantBytes, gotBytes)
}
