package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// APIError is a non-2xx response from MOCO. Most error responses have an empty body;
// 422 responses carry {"errors": [...] | {...}} or {"message": "..."}.
type APIError struct {
	Method  string
	Path    string
	Status  int
	Message string // from the response body, if any
}

func newAPIError(method, path string, status int, body []byte) *APIError {
	return &APIError{Method: method, Path: path, Status: status, Message: parseErrorBody(body)}
}

func parseErrorBody(body []byte) string {
	var b struct {
		Errors  json.RawMessage `json:"errors"`
		Message string          `json:"message"`
	}
	if json.Unmarshal(body, &b) != nil {
		return ""
	}
	if b.Message != "" {
		return b.Message
	}
	var list []string
	if json.Unmarshal(b.Errors, &list) == nil {
		return strings.Join(list, "; ")
	}
	var fields map[string]any
	if json.Unmarshal(b.Errors, &fields) == nil {
		var parts []string
		for k, v := range fields {
			parts = append(parts, fmt.Sprintf("%s: %v", k, v))
		}
		sort.Strings(parts)
		return strings.Join(parts, "; ")
	}
	return ""
}

func (e *APIError) Error() string {
	var hint string
	switch e.Status {
	case http.StatusUnauthorized:
		hint = "the API token was rejected — run `moco login`"
	case http.StatusForbidden:
		hint = "your API token is not allowed to do this"
	case http.StatusNotFound:
		hint = "not found"
	case http.StatusLocked:
		hint = "locked (the period may be closed in MOCO)"
	case http.StatusUnprocessableEntity:
		hint = "MOCO rejected the data"
	case http.StatusTooManyRequests:
		hint = "rate limit exceeded, try again in a minute"
	default:
		if e.Status >= 500 {
			hint = "MOCO server error"
		} else {
			hint = http.StatusText(e.Status)
		}
	}
	msg := fmt.Sprintf("MOCO %s %s: %d %s", e.Method, e.Path, e.Status, hint)
	if e.Message != "" {
		msg += ": " + e.Message
	}
	return msg
}

// NetworkError means MOCO could not be reached at all.
type NetworkError struct{ Err error }

func (e *NetworkError) Error() string { return "MOCO not reachable: " + e.Err.Error() }
func (e *NetworkError) Unwrap() error { return e.Err }

// IsUnreachable reports whether err means the request may not have reached MOCO
// (network failure or 5xx), i.e. the write can be queued and retried later.
func IsUnreachable(err error) bool {
	var ne *NetworkError
	if errors.As(err, &ne) {
		return true
	}
	var ae *APIError
	return errors.As(err, &ae) && ae.Status >= 500
}

// IsStatus reports whether err is an APIError with the given status.
func IsStatus(err error, status int) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == status
}
