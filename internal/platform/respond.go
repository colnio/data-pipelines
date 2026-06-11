package platform

import (
	"encoding/json"
	"net/http"
)

// writeError serializes an *ErrorModel to the response using the structured
// envelope {code,message,details}. Used by chi-level middleware (huma handlers
// return the error and the framework serializes it through the same model).
func writeError(w http.ResponseWriter, e *ErrorModel) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.status)
	_ = json.NewEncoder(w).Encode(e)
}
