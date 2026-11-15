package protocol

import "testing"

func TestAccountOnboardingAndResets(t *testing.T) {
	s := AccountStatus{State: PasswordRequired}
	for _, step := range []struct {
		action AccountAction
		want   AccountStatus
	}{
		{ReplacePassword, AccountStatus{State: TOTPRequired}},
		{VerifyEnrollment, AccountStatus{State: AccountActive, TOTPEnrolled: true}},
		{ResetPassword, AccountStatus{State: PasswordRequired, TOTPEnrolled: true}},
		{ReplacePassword, AccountStatus{State: AccountActive, TOTPEnrolled: true}},
		{ResetTOTP, AccountStatus{State: TOTPRequired}},
		{ResetPassword, AccountStatus{State: PasswordRequired}},
		{ResetTOTP, AccountStatus{State: PasswordRequired}},
		{DisableAccount, AccountStatus{State: PasswordRequired, Disabled: true}},
		{ResetPassword, AccountStatus{State: PasswordRequired, Disabled: true}},
		{EnableAccount, AccountStatus{State: PasswordRequired}},
	} {
		next, err := s.Apply(step.action)
		if err != nil || next != step.want {
			t.Fatalf("%+v -> %s = %+v, %v; want %+v", s, step.action, next, err, step.want)
		}
		s = next
	}
}

func TestAccountRejectsOnboardingBypass(t *testing.T) {
	for _, tc := range []struct {
		s      AccountStatus
		action AccountAction
	}{
		{AccountStatus{State: PasswordRequired}, VerifyEnrollment},
		{AccountStatus{State: TOTPRequired}, ReplacePassword},
		{AccountStatus{State: AccountActive, TOTPEnrolled: true}, ReplacePassword},
		{AccountStatus{State: PasswordRequired, Disabled: true}, ReplacePassword},
		{AccountStatus{State: TOTPRequired, Disabled: true}, VerifyEnrollment},
		{AccountStatus{State: PasswordRequired}, "future"},
		{AccountStatus{State: AccountActive}, ResetPassword},
		{AccountStatus{State: TOTPRequired, TOTPEnrolled: true}, ResetPassword},
		{AccountStatus{}, ResetPassword},
	} {
		if next, err := tc.s.Apply(tc.action); err == nil || next != (AccountStatus{}) {
			t.Fatalf("invalid account transition accepted: %+v -> %s", tc.s, tc.action)
		}
	}
}

func TestUploadLifecycleAndRecovery(t *testing.T) {
	for _, path := range [][]UploadState{
		{UploadOpen, UploadFinalizing, UploadCommitted},
		{UploadOpen, UploadFinalizing, UploadConflicted},
		{UploadOpen, UploadFinalizing, UploadOpen, UploadFinalizing, UploadCommitted},
		{UploadOpen, UploadCancelled}, {UploadOpen, UploadExpired},
	} {
		for i := 1; i < len(path); i++ {
			if err := ValidateUploadTransition(path[i-1], path[i]); err != nil {
				t.Fatal(err)
			}
		}
	}
	states := []UploadState{UploadOpen, UploadFinalizing, UploadCommitted, UploadConflicted, UploadCancelled, UploadExpired, "future", ""}
	for _, state := range states {
		if ValidateUploadTransition(state, state) == nil {
			t.Fatal("replay treated as a mutation")
		}
		for _, terminal := range []UploadState{UploadCommitted, UploadConflicted, UploadCancelled, UploadExpired} {
			if ValidateUploadTransition(terminal, state) == nil {
				t.Fatalf("terminal upload changed: %s -> %s", terminal, state)
			}
		}
	}
	for _, pair := range [][2]UploadState{{UploadOpen, UploadCommitted}, {UploadFinalizing, UploadCancelled}, {UploadFinalizing, UploadExpired}, {"future", UploadOpen}} {
		if ValidateUploadTransition(pair[0], pair[1]) == nil {
			t.Fatalf("unsafe upload transition accepted: %v", pair)
		}
	}
}

func TestQueuePauseWaitRestartAndPartialSuccess(t *testing.T) {
	// Scanning checkpoints to queued before the scheduler starts transfers.
	path := []QueueState{Queued, Scanning, Queued, Running, Paused, Queued, Running}
	for _, waiting := range []QueueState{WaitingNetwork, WaitingAuthentication, WaitingVault, WaitingCapacity, RetryScheduled} {
		path = append(path, waiting, Queued, Running)
	}
	path = append(path, Queued, Scanning, Partial)
	for i := 1; i < len(path); i++ {
		if err := ValidateQueueTransition(path[i-1], path[i]); err != nil {
			t.Fatalf("%s -> %s: %v", path[i-1], path[i], err)
		}
	}
	for _, terminal := range []QueueState{Complete, Partial, Cancelled, Failed} {
		for _, to := range []QueueState{Queued, Running, Cancelled, terminal} {
			if ValidateQueueTransition(terminal, to) == nil {
				t.Fatalf("terminal queue job changed: %s -> %s", terminal, to)
			}
		}
	}
	for _, pair := range [][2]QueueState{{Queued, Complete}, {WaitingVault, Running}, {Paused, Running}, {Running, Running}, {"future", Queued}, {Running, "future"}, {Scanning, Running}} {
		if ValidateQueueTransition(pair[0], pair[1]) == nil {
			t.Fatalf("unsafe queue transition accepted: %v", pair)
		}
	}
}
