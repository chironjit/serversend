package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"github.com/chironjit/serversend/shared/protocol"
	"strings"
	"unicode"
	"unicode/utf8"
)

func validUsername(username string) bool {
	if len(username) < 1 || len(username) > 128 {
		return false
	}
	// Canonical ASCII names avoid case/Unicode aliases across clients. Names
	// are stored case-insensitively by lowercasing at the API boundary below.
	for _, c := range username {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func ValidatePassword(password string) error {
	if !utf8.ValidString(password) || len(password) > 256 || utf8.RuneCountInString(password) < 12 || utf8.RuneCountInString(password) > 128 || strings.ContainsFunc(password, unicode.IsControl) || strings.TrimSpace(password) == "" {
		return ErrPasswordPolicy
	}
	return nil
}

func newID() protocol.ID       { return protocol.ID(hex.EncodeToString(randomBytes(16))) }
func newSecret() string        { return base64.RawURLEncoding.EncodeToString(randomBytes(32)) }
func randomBytes(n int) []byte { value := make([]byte, n); _, _ = rand.Read(value); return value }
func tokenHash(token string) string {
	sum := sha256.Sum256([]byte("ServerSend restricted authentication v1\x00" + token))
	return hex.EncodeToString(sum[:])
}

func validToken(token string) bool {
	value, err := base64.RawURLEncoding.DecodeString(token)
	return err == nil && len(value) == 32 && base64.RawURLEncoding.EncodeToString(value) == token
}
