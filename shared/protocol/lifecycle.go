package protocol

import "errors"

type AccountState string

const (
	PasswordRequired AccountState = "password_required"
	TOTPRequired     AccountState = "totp_required"
	AccountActive    AccountState = "active"
)

// Disabled is independent of onboarding. A reset cannot enable an account.
type AccountStatus struct {
	State        AccountState `json:"state"`
	TOTPEnrolled bool         `json:"totp_enrolled"`
	Disabled     bool         `json:"disabled"`
}

func (s AccountStatus) Validate() error {
	if s.State != PasswordRequired && s.State != TOTPRequired && s.State != AccountActive {
		return errors.New("unknown account state")
	}
	if (s.State == AccountActive && !s.TOTPEnrolled) || (s.State == TOTPRequired && s.TOTPEnrolled) {
		return errors.New("account state disagrees with TOTP enrollment")
	}
	return nil
}

type AccountAction string

const (
	ReplacePassword  AccountAction = "replace_password"
	VerifyEnrollment AccountAction = "verify_enrollment"
	ResetPassword    AccountAction = "reset_password"
	ResetTOTP        AccountAction = "reset_totp"
	DisableAccount   AccountAction = "disable"
	EnableAccount    AccountAction = "enable"
)

// Apply describes state only. Callers must authorize the action, consume
// credentials atomically, audit resets, and revoke sessions as specified.
func (s AccountStatus) Apply(action AccountAction) (AccountStatus, error) {
	if err := s.Validate(); err != nil {
		return AccountStatus{}, err
	}
	next := s
	switch action {
	case ReplacePassword:
		if s.Disabled || s.State != PasswordRequired {
			return AccountStatus{}, errors.New("password replacement is not allowed")
		}
		next.State = TOTPRequired
		if s.TOTPEnrolled {
			next.State = AccountActive
		}
	case VerifyEnrollment:
		if s.Disabled || s.State != TOTPRequired {
			return AccountStatus{}, errors.New("enrollment is not allowed")
		}
		next.State, next.TOTPEnrolled = AccountActive, true
	case ResetPassword:
		next.State = PasswordRequired
	case ResetTOTP:
		next.TOTPEnrolled = false
		if s.State != PasswordRequired {
			next.State = TOTPRequired
		}
	case DisableAccount:
		next.Disabled = true
	case EnableAccount:
		next.Disabled = false
	default:
		return AccountStatus{}, errors.New("unknown account action")
	}
	return next, nil
}

type UploadState string

const (
	UploadOpen       UploadState = "open"
	UploadFinalizing UploadState = "finalizing"
	UploadCommitted  UploadState = "committed"
	UploadConflicted UploadState = "conflicted"
	UploadCancelled  UploadState = "cancelled"
	UploadExpired    UploadState = "expired"
)

func (s UploadState) Validate() error {
	switch s {
	case UploadOpen, UploadFinalizing, UploadCommitted, UploadConflicted, UploadCancelled, UploadExpired:
		return nil
	default:
		return errors.New("unknown upload state")
	}
}

// ValidateUploadTransition excludes replay (which returns the recorded result
// without changing state). Finalization owns its reservation until recovery
// commits or rolls it back to open. Cancel/expire may only win from open.
func ValidateUploadTransition(from, to UploadState) error {
	if from == UploadOpen && (to == UploadFinalizing || to == UploadCancelled || to == UploadExpired) ||
		from == UploadFinalizing && (to == UploadOpen || to == UploadCommitted || to == UploadConflicted) {
		return nil
	}
	return errors.New("invalid upload transition")
}

type QueueState string

const (
	Queued                QueueState = "queued"
	Scanning              QueueState = "scanning"
	Running               QueueState = "running"
	Paused                QueueState = "paused"
	WaitingNetwork        QueueState = "waiting_network"
	WaitingAuthentication QueueState = "waiting_authentication"
	WaitingVault          QueueState = "waiting_vault"
	WaitingCapacity       QueueState = "waiting_capacity"
	RetryScheduled        QueueState = "retry_scheduled"
	Complete              QueueState = "complete"
	Partial               QueueState = "partial"
	Cancelled             QueueState = "cancelled"
	Failed                QueueState = "failed"
)

func (s QueueState) Validate() error {
	switch s {
	case Queued, Scanning, Running, Paused, WaitingNetwork, WaitingAuthentication, WaitingVault, WaitingCapacity, RetryScheduled, Complete, Partial, Cancelled, Failed:
		return nil
	default:
		return errors.New("unknown queue state")
	}
}

// Resume/restart always re-enters queued and reconciles persisted work before
// scanning or running. Terminal jobs are immutable; retry creates a new job.
func ValidateQueueTransition(from, to QueueState) error {
	if from.Validate() != nil || to.Validate() != nil || from == to {
		return errors.New("invalid queue transition")
	}
	switch from {
	case Queued:
		if to == Scanning || to == Running || to == Paused || to == Cancelled {
			return nil
		}
	case Scanning, Running:
		switch to {
		case Queued, Paused, WaitingNetwork, WaitingAuthentication, WaitingVault, WaitingCapacity, RetryScheduled, Complete, Partial, Cancelled, Failed:
			return nil
		}
	case Paused, WaitingNetwork, WaitingAuthentication, WaitingVault, WaitingCapacity, RetryScheduled:
		if to == Queued || to == Paused || to == Cancelled || to == Failed {
			return nil
		}
	}
	return errors.New("invalid queue transition")
}
