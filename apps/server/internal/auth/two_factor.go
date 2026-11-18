package auth

import (
	"context"
	"database/sql"
	"errors"

	"github.com/chironjit/serversend/apps/server/internal/auth/twofactor"
	"github.com/chironjit/serversend/apps/server/internal/database"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
)

// TOTPEnrollment is sensitive provisional material for the enrolling user.
type TOTPEnrollment = api.TOTPEnrollment

// Authenticated records a completed password and second-factor login. It is
// input for the session layer, which must bind Epoch into what it issues and
// recheck it on every request; by itself it authorizes nothing.
type Authenticated struct {
	AccountID              protocol.ID
	Epoch                  int64
	UsedRecoveryCode       bool
	RecoveryCodesRemaining int
}

const enrollmentQRSize = 256

func (s *Service) scope(account protocol.ID) protocol.Scope {
	return protocol.Scope{ServerID: s.serverID, AccountID: account}
}

// secondFactorChallenge loads a live challenge of exactly one scope. Every
// failure is the same credential error, including an account under backoff.
func (s *Service) secondFactorChallenge(ctx context.Context, token, scope string, state protocol.AccountState) (database.Credentials, database.AuthChallenge, error) {
	c, ch, err := s.repo.Challenge(ctx, tokenHash(token), s.now())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, database.ErrAuthConflict) {
			err = ErrCredentials
		}
		return c, ch, err
	}
	if ch.Scope != scope || c.Status.State != state || c.SecondFactorBlockedUntil > s.now().UnixMilli() {
		return c, ch, ErrCredentials
	}
	return c, ch, nil
}

func (s *Service) secondFactorFailed(ctx context.Context, ch database.AuthChallenge) error {
	if err := s.repo.RecordSecondFactorFailure(ctx, ch, s.now().UTC()); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return ErrCredentials
}

func conflictAsCredentials(err error) error {
	if errors.Is(err, database.ErrAuthConflict) {
		return ErrCredentials
	}
	return err
}

// BeginTOTPEnrollment issues fresh provisional material. Calling it again
// discards the previous unconfirmed secret, so a lost response or restarted
// client simply starts over; nothing is active until confirmation.
func (s *Service) BeginTOTPEnrollment(ctx context.Context, token string) (TOTPEnrollment, error) {
	if s.protector == nil {
		return TOTPEnrollment{}, ErrTwoFactorUnavailable
	}
	if !validToken(token) {
		return TOTPEnrollment{}, ErrCredentials
	}
	release, err := s.acquire()
	if err != nil {
		return TOTPEnrollment{}, err
	}
	defer release()
	c, ch, err := s.secondFactorChallenge(ctx, token, "enroll_totp", protocol.TOTPRequired)
	if err != nil {
		return TOTPEnrollment{}, err
	}
	enrollment, err := twofactor.Enroll(c.Username, enrollmentQRSize)
	if err != nil {
		return TOTPEnrollment{}, err
	}
	envelope, err := s.protector.Seal(s.scope(c.ID), enrollment.Secret)
	if err != nil {
		return TOTPEnrollment{}, err
	}
	if err := s.repo.StoreProvisionalTOTP(ctx, ch, envelope, s.now().UTC()); err != nil {
		return TOTPEnrollment{}, conflictAsCredentials(err)
	}
	return TOTPEnrollment{Secret: enrollment.Secret, URI: enrollment.URI, QRPNG: enrollment.QRPNG}, nil
}

// ConfirmTOTPEnrollment activates the provisional secret after one valid code
// and returns the recovery codes exactly once. Only their hashes are stored.
func (s *Service) ConfirmTOTPEnrollment(ctx context.Context, token, code string) ([]string, error) {
	if s.protector == nil {
		return nil, ErrTwoFactorUnavailable
	}
	if !validToken(token) {
		return nil, ErrCredentials
	}
	release, err := s.acquire()
	if err != nil {
		return nil, err
	}
	defer release()
	c, ch, err := s.secondFactorChallenge(ctx, token, "enroll_totp", protocol.TOTPRequired)
	if err != nil {
		return nil, err
	}
	stored, err := s.repo.TOTPSecret(ctx, c.ID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && stored.Confirmed {
		return nil, ErrCredentials
	}
	if err != nil {
		return nil, err
	}
	secret, err := s.protector.Open(s.scope(c.ID), stored.Envelope)
	if err != nil {
		return nil, ErrTwoFactorUnavailable
	}
	step, err := twofactor.Verify(secret, code, s.now(), stored.LastStep)
	if err != nil {
		return nil, s.secondFactorFailed(ctx, ch)
	}
	codes, err := twofactor.GenerateRecoveryCodes(s.scope(c.ID))
	if err != nil {
		return nil, err
	}
	display, hashes := splitRecoveryCodes(codes)
	if err := s.repo.ActivateTOTP(ctx, ch, stored.Envelope, step, hashes, s.now().UTC()); err != nil {
		return nil, conflictAsCredentials(err)
	}
	return display, nil
}

// VerifySecondFactor accepts a six-digit TOTP code or one recovery code. The
// replay watermark or the recovery-code row is consumed in the transaction
// that spends the challenge, before any result is returned.
func (s *Service) VerifySecondFactor(ctx context.Context, token, code string) (Authenticated, error) {
	if s.protector == nil {
		return Authenticated{}, ErrTwoFactorUnavailable
	}
	if !validToken(token) || code == "" || len(code) > 64 {
		return Authenticated{}, ErrCredentials
	}
	release, err := s.acquire()
	if err != nil {
		return Authenticated{}, err
	}
	defer release()
	c, ch, err := s.secondFactorChallenge(ctx, token, "second_factor", protocol.AccountActive)
	if err != nil {
		return Authenticated{}, err
	}
	result := Authenticated{AccountID: c.ID, Epoch: c.Epoch}
	if len(code) != 6 {
		hash, err := twofactor.HashRecoveryCode(s.scope(c.ID), code)
		if err != nil {
			return Authenticated{}, s.secondFactorFailed(ctx, ch)
		}
		remaining, err := s.repo.ConsumeRecoveryCode(ctx, ch, hash[:], s.now().UTC())
		if errors.Is(err, database.ErrAuthConflict) {
			return Authenticated{}, s.secondFactorFailed(ctx, ch)
		}
		if err != nil {
			return Authenticated{}, err
		}
		result.UsedRecoveryCode, result.RecoveryCodesRemaining = true, remaining
		return result, nil
	}
	stored, step, err := s.verifyTOTP(ctx, ch, code)
	if err != nil {
		return Authenticated{}, err
	}
	if err := s.repo.ConsumeTOTPStep(ctx, ch, stored.Envelope, step, s.now().UTC()); err != nil {
		return Authenticated{}, conflictAsCredentials(err)
	}
	return result, nil
}

func (s *Service) verifyTOTP(ctx context.Context, ch database.AuthChallenge, code string) (database.TOTPSecret, int64, error) {
	stored, err := s.repo.TOTPSecret(ctx, ch.AccountID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !stored.Confirmed {
		return stored, 0, ErrCredentials
	}
	if err != nil {
		return stored, 0, err
	}
	secret, err := s.protector.Open(s.scope(ch.AccountID), stored.Envelope)
	if err != nil {
		return stored, 0, ErrTwoFactorUnavailable
	}
	step, err := twofactor.Verify(secret, code, s.now(), stored.LastStep)
	if err != nil {
		return stored, 0, s.secondFactorFailed(ctx, ch)
	}
	return stored, step, nil
}

func (s *Service) ReplaceRecoveryCodes(ctx context.Context, token, password, code string) ([]string, error) {
	session, err := s.Authorize(ctx, token)
	if err != nil {
		return nil, err
	}
	if s.protector == nil {
		return nil, ErrTwoFactorUnavailable
	}
	if len(code) != 6 {
		return nil, ErrCredentials
	}
	attempt, err := s.AuthenticatePassword(ctx, session.Username, password)
	if err != nil {
		return nil, err
	}
	_, ch, err := s.secondFactorChallenge(ctx, attempt.Challenge, "second_factor", protocol.AccountActive)
	if err != nil {
		return nil, err
	}
	stored, step, err := s.verifyTOTP(ctx, ch, code)
	if err != nil {
		return nil, err
	}
	codes, err := twofactor.GenerateRecoveryCodes(s.scope(session.AccountID))
	if err != nil {
		return nil, err
	}
	display, hashes := splitRecoveryCodes(codes)
	if err := s.repo.ReplaceRecoveryCodes(ctx, ch, stored.Envelope, step, s.now().UTC(), accessHash(token), hashes); err != nil {
		return nil, refreshError(err)
	}
	return display, nil
}

// ResetTOTP is the administrator's lost-authenticator recovery. It is distinct
// from a password reset: the password is untouched, the old secret and all
// recovery codes are destroyed, and the user must enroll again before access.
func (s *Service) ResetTOTP(ctx context.Context, actor Principal, target protocol.ID) error {
	if !s.reauthenticated(actor) {
		return ErrForbidden
	}
	if target.Validate() != nil {
		return ErrInput
	}
	return s.repo.ResetTOTP(ctx, actor.accountID, actor.epoch, target, s.now().UTC())
}

func splitRecoveryCodes(codes []twofactor.RecoveryCode) ([]string, [][]byte) {
	display, hashes := make([]string, len(codes)), make([][]byte, len(codes))
	for i := range codes {
		display[i], hashes[i] = codes[i].Code, codes[i].Hash[:]
	}
	return display, hashes
}
