// Package twofactor provides TOTP enrollment and verification helpers. It does
// not authenticate accounts or persist enrollment and replay state.
package twofactor

import "errors"

var (
	ErrInvalidAccount = errors.New("invalid TOTP account label")
	ErrInvalidQRSize  = errors.New("TOTP QR size must be between 256 and 1024 pixels")
	ErrEnrollment     = errors.New("could not generate TOTP enrollment")
	ErrInvalidCode    = errors.New("invalid TOTP code")
	ErrInvalidSecret  = errors.New("invalid TOTP secret")
	ErrInvalidTime    = errors.New("TOTP verification time must not precede the Unix epoch")
	ErrInvalidStep    = errors.New("invalid TOTP replay state")
	ErrReplay         = errors.New("TOTP time step already consumed")
)
