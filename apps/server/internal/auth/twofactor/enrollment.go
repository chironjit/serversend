package twofactor

import (
	"bytes"
	"crypto/rand"
	"image/png"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

const (
	Issuer      = "ServerSend"
	MinQRSize   = 256
	MaxQRSize   = 1024
	secretBytes = 20
)

// Enrollment contains sensitive material. Do not log it or expose it outside
// the authenticated user's provisional enrollment flow. Encrypt Secret at rest.
type Enrollment struct {
	Secret string
	URI    string
	QRPNG  []byte
}

// Enroll creates a fresh enrollment using cryptographically secure randomness.
// qrSize is the width and height in pixels. The account label is supplied
// explicitly by the caller and should distinguish the account and server.
func Enroll(accountLabel string, qrSize int) (Enrollment, error) {
	return enroll(accountLabel, qrSize, rand.Reader)
}

func enroll(accountLabel string, qrSize int, random io.Reader) (Enrollment, error) {
	if accountLabel == "" || len(accountLabel) > 128 || !utf8.ValidString(accountLabel) || strings.TrimSpace(accountLabel) != accountLabel || strings.ContainsRune(accountLabel, ':') || strings.ContainsFunc(accountLabel, unicode.IsControl) {
		return Enrollment{}, ErrInvalidAccount
	}
	if qrSize < MinQRSize || qrSize > MaxQRSize {
		return Enrollment{}, ErrInvalidQRSize
	}
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer: Issuer, AccountName: accountLabel, Period: 30,
		SecretSize: secretBytes, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
		Rand: random,
	})
	if err != nil {
		return Enrollment{}, ErrEnrollment
	}
	img, err := key.Image(qrSize, qrSize)
	if err != nil {
		return Enrollment{}, ErrEnrollment
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		return Enrollment{}, ErrEnrollment
	}
	return Enrollment{Secret: key.Secret(), URI: key.URL(), QRPNG: encoded.Bytes()}, nil
}
