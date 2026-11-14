// Package api defines versioned ServerSend JSON contracts.
package api

import (
	"encoding/json"
	"errors"
	"io"
)

const BasePath = "/api/v1"

var ErrResponseTooLarge = errors.New("API response exceeds size limit")

// decode permits additive fields for forward compatibility, while rejecting
// malformed JSON, trailing documents and oversized bodies before decoding.
func decode(r io.Reader, limit int64, target any) error {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return err
	}
	if int64(len(body)) > limit {
		return ErrResponseTooLarge
	}
	return json.Unmarshal(body, target)
}
