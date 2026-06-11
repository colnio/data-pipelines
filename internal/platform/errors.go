package platform

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// ErrorModel is the structured error envelope used across the API:
// {"code": "...", "message": "...", "details": {...}}.
type ErrorModel struct {
	Code    string         `json:"code" doc:"Machine-readable error code, e.g. sample.not_found"`
	Message string         `json:"message" doc:"Human-readable error message"`
	Details map[string]any `json:"details,omitempty" doc:"Optional structured detail"`
	status  int
}

func (e *ErrorModel) Error() string { return e.Message }

// GetStatus lets huma read the intended HTTP status from the error.
func (e *ErrorModel) GetStatus() int { return e.status }

// NewError builds an ErrorModel from a status and message. It is wired into
// huma via huma.NewError so framework-generated errors share the envelope.
func NewError(status int, message string, code string) *ErrorModel {
	if code == "" {
		code = defaultCodeForStatus(status)
	}
	return &ErrorModel{Code: code, Message: message, status: status}
}

// Errorf is a convenience constructor with a code.
func Errorf(status int, code, message string) *ErrorModel {
	return &ErrorModel{Code: code, Message: message, status: status}
}

// WithDetails attaches structured detail and returns the error for chaining.
func (e *ErrorModel) WithDetails(d map[string]any) *ErrorModel {
	e.Details = d
	return e
}

// humaNewError is the function huma calls to build errors. We map huma's
// (status, message, errs...) signature into our envelope.
func humaNewError(status int, message string, errs ...error) huma.StatusError {
	e := NewError(status, message, "")
	if len(errs) > 0 {
		details := map[string]any{}
		var fields []any
		for _, err := range errs {
			if err == nil {
				continue
			}
			fields = append(fields, err.Error())
		}
		if len(fields) > 0 {
			details["errors"] = fields
			e.Details = details
		}
	}
	return e
}

func defaultCodeForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusConflict:
		return "conflict"
	case http.StatusPreconditionFailed:
		return "precondition_failed"
	case http.StatusUnprocessableEntity:
		return "unprocessable_entity"
	case http.StatusTooManyRequests:
		return "rate_limited"
	default:
		if status >= 500 {
			return "internal_error"
		}
		return "error"
	}
}

// Common helpers used by modules.
func NotFound(code, message string) *ErrorModel {
	return Errorf(http.StatusNotFound, code, message)
}
func Forbidden(message string) *ErrorModel {
	return Errorf(http.StatusForbidden, "forbidden", message)
}
func Unauthorized(message string) *ErrorModel {
	return Errorf(http.StatusUnauthorized, "unauthorized", message)
}
func BadRequest(code, message string) *ErrorModel {
	return Errorf(http.StatusBadRequest, code, message)
}
func Conflict(code, message string) *ErrorModel {
	return Errorf(http.StatusConflict, code, message)
}
