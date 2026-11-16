package twofactor

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"io"
	"strings"

	"github.com/chironjit/serversend/shared/protocol"
)

const (
	RecoveryCodeCount = 10
	recoveryCodeBytes = 16 // 128 independent random bits per code
)

// RecoveryCode contains a one-time display value and the hash to persist.
// Plaintext must be returned only to the authenticated account, then discarded;
// never log the structure or persist Code. A hash does not enforce single use:
// the caller must consume its database row atomically before granting access.
type RecoveryCode struct {
	Code string
	Hash [sha256.Size]byte
}

// GenerateRecoveryCodes issues ten independent high-entropy codes, displayed as
// four groups of eight hexadecimal characters. Generation failures return no
// partial set. The caller replaces any prior set in one account transaction.
func GenerateRecoveryCodes(scope protocol.Scope) ([]RecoveryCode, error) {
	return generateRecoveryCodes(scope, rand.Reader)
}

func generateRecoveryCodes(scope protocol.Scope, random io.Reader) ([]RecoveryCode, error) {
	if err := scope.Validate(); err != nil {
		return nil, ErrInvalidScope
	}
	result := make([]RecoveryCode, 0, RecoveryCodeCount)
	seen := make(map[[sha256.Size]byte]bool, RecoveryCodeCount)
	for range RecoveryCodeCount {
		var entropy [recoveryCodeBytes]byte
		if _, err := io.ReadFull(random, entropy[:]); err != nil {
			return nil, ErrRecoveryCodes
		}
		compact := strings.ToUpper(hex.EncodeToString(entropy[:]))
		code := compact[:8] + "-" + compact[8:16] + "-" + compact[16:24] + "-" + compact[24:]
		hash, err := HashRecoveryCode(scope, code)
		if err != nil || seen[hash] {
			// A duplicate means the random source failed. Do not silently issue a
			// smaller set or let a broken source cause an unbounded retry loop.
			return nil, ErrRecoveryCodes
		}
		seen[hash] = true
		result = append(result, RecoveryCode{Code: code, Hash: hash})
	}
	return result, nil
}

// HashRecoveryCode normalizes ASCII case and optional outer whitespace. It
// accepts either 32 compact hex digits or the exact four-group display format;
// interior whitespace, misplaced separators and non-ASCII digits are rejected.
// Scope binding prevents identical codes from sharing a stored hash across
// independent accounts or servers. SHA-256 is appropriate for these random
// 128-bit secrets; these are not user-selected passwords.
func HashRecoveryCode(scope protocol.Scope, code string) ([sha256.Size]byte, error) {
	if err := scope.Validate(); err != nil {
		return [sha256.Size]byte{}, ErrInvalidScope
	}
	// Bound input before whitespace normalization or allocations.
	if len(code) > 64 {
		return [sha256.Size]byte{}, ErrRecoveryCode
	}
	code = strings.TrimSpace(code)
	if len(code) == 35 {
		if code[8] != '-' || code[17] != '-' || code[26] != '-' {
			return [sha256.Size]byte{}, ErrRecoveryCode
		}
		code = code[:8] + code[9:17] + code[18:26] + code[27:]
	}
	if len(code) != 32 {
		return [sha256.Size]byte{}, ErrRecoveryCode
	}
	var normalized [32]byte
	for i := range code {
		c := code[i]
		if c >= 'a' && c <= 'f' {
			c -= 'a' - 'A'
		}
		if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'F') {
			return [sha256.Size]byte{}, ErrRecoveryCode
		}
		normalized[i] = c
	}
	return sha256.Sum256([]byte("ServerSend/recovery-code/v1\x00" + string(scope.ServerID) + string(scope.AccountID) + string(normalized[:]))), nil
}

// MatchRecoveryCode uses a constant-time digest comparison after bounded input
// validation. Rejected input and foreign scopes simply do not match. Matching
// does not consume the code; durable atomic consumption remains mandatory.
func MatchRecoveryCode(scope protocol.Scope, code string, expected [sha256.Size]byte) bool {
	actual, err := HashRecoveryCode(scope, code)
	return err == nil && subtle.ConstantTimeCompare(actual[:], expected[:]) == 1
}
