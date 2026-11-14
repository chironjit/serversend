package api

import (
	"errors"
	"io"
	"strings"
)

const MaxErrorResponseBytes = 16 * 1024

type ErrorCode string

const (
	InvalidRequest         ErrorCode = "invalid_request"
	AuthenticationRequired ErrorCode = "authentication_required"
	PermissionDenied       ErrorCode = "permission_denied"
	NotFound               ErrorCode = "not_found"
	Conflict               ErrorCode = "conflict"
	CapacityExhausted      ErrorCode = "capacity_exhausted"
	Overloaded             ErrorCode = "overloaded"
	Unavailable            ErrorCode = "unavailable"
	UnsupportedVersion     ErrorCode = "unsupported_version"
	UnsupportedFormat      ErrorCode = "unsupported_format"
	SourceMissing          ErrorCode = "source_missing"
	InternalError          ErrorCode = "internal_error"
)

// Error contains a safe, user-facing message, never a raw internal error or
// credentials. Producers are responsible for redaction before serialization.
type Error struct {
	Code              ErrorCode `json:"code"`
	Message           string    `json:"message"`
	RequestID         string    `json:"request_id,omitempty"`
	RetryAfterSeconds int64     `json:"retry_after_seconds,omitempty"`
}

type ErrorResponse struct {
	Error Error `json:"error"`
}

func (e Error) Error() string {
	return string(e.Code) + ": " + e.Message
}

// Retryable classifies transient errors only. The caller must still check that
// replaying its particular operation is safe and honor retry/backoff budgets.
// Unknown future codes are preserved but are not automatically retryable.
func (e Error) Retryable() bool {
	return e.Code == Overloaded || e.Code == Unavailable
}

func (e Error) Validate() error {
	if strings.TrimSpace(string(e.Code)) == "" || strings.TrimSpace(e.Message) == "" {
		return errors.New("error code and message are required")
	}
	if e.RetryAfterSeconds < 0 || (e.RetryAfterSeconds > 0 && !e.Retryable()) {
		return errors.New("retry delay requires a transient error and nonnegative seconds")
	}
	return nil
}

func DecodeError(r io.Reader) (Error, error) {
	var response ErrorResponse
	if err := decode(r, MaxErrorResponseBytes, &response); err != nil {
		return Error{}, err
	}
	if err := response.Error.Validate(); err != nil {
		return Error{}, err
	}
	return response.Error, nil
}
