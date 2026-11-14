package api

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestErrorContract(t *testing.T) {
	want := Error{Code: Overloaded, Message: "Try again later", RequestID: "request-1", RetryAfterSeconds: 5}
	encoded, err := json.Marshal(ErrorResponse{Error: want})
	if err != nil {
		t.Fatal(err)
	}
	const wire = `{"error":{"code":"overloaded","message":"Try again later","request_id":"request-1","retry_after_seconds":5}}`
	if string(encoded) != wire {
		t.Fatalf("wire contract changed: %s", encoded)
	}
	got, err := DecodeError(strings.NewReader(wire))
	if err != nil || got != want {
		t.Fatalf("got (%#v, %v), want %#v", got, err, want)
	}
	if got.Error() != "overloaded: Try again later" {
		t.Fatalf("unexpected error display: %s", got.Error())
	}
	encoded, err = json.Marshal(ErrorResponse{Error: Error{Code: NotFound, Message: "Not found"}})
	if err != nil || string(encoded) != `{"error":{"code":"not_found","message":"Not found"}}` {
		t.Fatalf("optional fields not omitted: %s, %v", encoded, err)
	}
}

func TestErrorRetryPolicy(t *testing.T) {
	codes := []ErrorCode{InvalidRequest, AuthenticationRequired, PermissionDenied, NotFound, Conflict, CapacityExhausted, Overloaded, Unavailable, UnsupportedVersion, UnsupportedFormat, SourceMissing, InternalError, "future_error"}
	for _, code := range codes {
		t.Run(string(code), func(t *testing.T) {
			e := Error{Code: code, Message: "Safe public message"}
			transient := code == Overloaded || code == Unavailable
			if e.Retryable() != transient || e.Validate() != nil {
				t.Fatalf("unexpected retry classification or validation: %#v", e)
			}
			e.RetryAfterSeconds = 1
			if (e.Validate() == nil) != transient {
				t.Fatalf("retry delay accepted inconsistently for %q", code)
			}
			e.RetryAfterSeconds = -1
			if e.Validate() == nil {
				t.Fatal("negative retry delay accepted")
			}
		})
	}
}

func TestDecodeErrorInvalid(t *testing.T) {
	for _, body := range []string{
		"", "null", "{}", `{"error":null}`, `[]`, `{`,
		`{"error":{"code":"","message":"failure"}}`,
		`{"error":{"code":"   ","message":"failure"}}`,
		`{"error":{"code":"conflict","message":" "}}`,
		`{"error":{"code":"conflict","message":"failure","retry_after_seconds":1}}`,
		`{"error":{"code":"overloaded","message":"failure","retry_after_seconds":-1}}`,
		`{"error":{"code":"overloaded","message":"failure","retry_after_seconds":0.5}}`,
		`{"error":{"code":"overloaded","message":"failure","retry_after_seconds":9223372036854775808}}`,
		`{"error":{"code":"conflict","message":"failure"}}{}`,
	} {
		t.Run(body, func(t *testing.T) {
			got, err := DecodeError(strings.NewReader(body))
			if err == nil || got != (Error{}) {
				t.Fatalf("got (%#v, %v), want zero and error", got, err)
			}
		})
	}
}

func TestDecodeErrorCompatibilityAndBounds(t *testing.T) {
	const body = `{"future":true,"error":{"code":"future_error","message":"future message","extra":true}}`
	got, err := DecodeError(strings.NewReader(body))
	if err != nil || got.Code != "future_error" || got.Retryable() {
		t.Fatalf("unknown error compatibility: %#v, %v", got, err)
	}
	padded := body + strings.Repeat(" ", MaxErrorResponseBytes-len(body))
	if _, err := DecodeError(strings.NewReader(padded)); err != nil {
		t.Fatalf("exact bound rejected: %v", err)
	}
	if _, err := DecodeError(strings.NewReader(padded + " ")); !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("got %v, want ErrResponseTooLarge", err)
	}
	if _, err := DecodeError(failedReader{}); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("got %v, want read failure", err)
	}
}
