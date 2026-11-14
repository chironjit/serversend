package twofactor

import (
	"encoding/base32"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/hotp"
)

// NoAcceptedStep is the initial replay watermark for a newly created secret.
const NoAcceptedStep int64 = -1

// Verify checks six ASCII digits against 30-second SHA1 TOTP counters within one
// step of now. It returns the newest matching step, strictly above lastAcceptedStep.
// The caller must atomically persist that step before granting authentication;
// concurrent calls with the same old watermark can both succeed here.
// All failures return NoAcceptedStep and an error without secret or code contents.
func Verify(secret, code string, now time.Time, lastAcceptedStep int64) (int64, error) {
	if now.Unix() < 0 {
		return NoAcceptedStep, ErrInvalidTime
	}
	if lastAcceptedStep < NoAcceptedStep {
		return NoAcceptedStep, ErrInvalidStep
	}
	if len(code) != 6 || strings.IndexFunc(code, func(r rune) bool { return r < '0' || r > '9' }) != -1 {
		return NoAcceptedStep, ErrInvalidCode
	}
	// Only the canonical 20-byte Base32 form issued by Enroll is accepted.
	if len(secret) != 32 {
		return NoAcceptedStep, ErrInvalidSecret
	}
	decoded, err := base32.StdEncoding.DecodeString(secret)
	if err != nil || len(decoded) != secretBytes {
		return NoAcceptedStep, ErrInvalidSecret
	}
	current := now.Unix() / 30
	for step := current + 1; step >= current-1 && step >= 0; step-- {
		valid, err := hotp.ValidateCustom(code, uint64(step), secret, hotp.ValidateOpts{
			Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
		})
		if err != nil {
			return NoAcceptedStep, ErrInvalidSecret
		}
		if valid {
			if step <= lastAcceptedStep {
				return NoAcceptedStep, ErrReplay
			}
			return step, nil
		}
	}
	return NoAcceptedStep, ErrInvalidCode
}
