// Package api defines versioned ServerSend JSON contracts.
package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
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
	if !utf8.Valid(body) {
		return errors.New("JSON body is not UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	if err := validateJSONValue(d, 0); err != nil {
		return err
	}
	return json.Unmarshal(body, target)
}

// Reject ambiguous member names before encoding/json's last-key-wins and
// case-insensitive field matching can hide conflicting intent. Depth is local
// policy and also bounds unknown additive fields.
func validateJSONValue(d *json.Decoder, depth int) error {
	if depth > 64 {
		return errors.New("JSON nesting exceeds limit")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, container := token.(json.Delim)
	if !container {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]bool)
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok {
				return errors.New("invalid JSON member name")
			}
			name = strings.Map(func(r rune) rune {
				minimum := r
				for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
					if next < minimum {
						minimum = next
					}
				}
				return minimum
			}, name)
			if seen[name] {
				return errors.New("duplicate JSON member name")
			}
			seen[name] = true
			if err := validateJSONValue(d, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := validateJSONValue(d, depth+1); err != nil {
				return err
			}
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	_, err = d.Token()
	return err
}
