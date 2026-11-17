package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/chironjit/serversend/apps/server/internal/database"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
)

func TestFullLoginDeduplicatesInstallationAndReplacesOldAuthority(t *testing.T) {
	s, clock, _, secret := refreshFixture(t)
	installation := protocol.ID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	loginDevice := func(id protocol.ID) api.Session {
		t.Helper()
		clock.Advance(30 * time.Second)
		next, err := s.CreateSession(t.Context(), login(t, s, "user", fixturePassword, "second_factor"), codeAt(t, secret, clock.Now()), "Laptop", api.SessionRequest{InstallationID: id, Platform: "linux", AppVersion: "test"})
		if err != nil {
			t.Fatal(err)
		}
		return next
	}
	first := loginDevice(installation)
	grant := rotate(t, s, rotation(first))
	next := loginDevice(installation)
	if first.DeviceID != next.DeviceID || first.RefreshCredential == next.RefreshCredential {
		t.Fatal("full login failed to replace same installation authority")
	}
	for _, token := range []string{first.AccessToken, grant.AccessToken} {
		if _, err := s.Authorize(t.Context(), token); !errors.Is(err, ErrCredentials) {
			t.Fatal("previous full login access remains valid", err)
		}
	}
	if _, err := s.Refresh(t.Context(), rotation(grant)); !errors.Is(err, ErrCredentials) {
		t.Fatal("old refresh still valid")
	}
	if _, err := s.Authorize(t.Context(), next.AccessToken); err != nil {
		t.Fatal("old credential attempt revoked new full login", err)
	}
	other := loginDevice("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if other.DeviceID == next.DeviceID {
		t.Fatal("different installations merged")
	}
	if err := s.RevokeOwnDevice(t.Context(), next.AccessToken, other.DeviceID, false); err != nil {
		t.Fatal(err)
	}
	resumed := loginDevice("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if resumed.DeviceID != other.DeviceID {
		t.Fatal("explicit full login duplicated revoked installation")
	}
	devices, err := s.OwnDevices(t.Context(), resumed.AccessToken)
	if err != nil || len(devices) != 3 {
		t.Fatal("unexpected device history", len(devices), err)
	}
}
func TestSelfServiceDevicesAccountIsolationAndRevocation(t *testing.T) {
	s, clock, first, secret := refreshFixture(t)
	clock.Advance(30 * time.Second)
	second, err := s.CreateSession(t.Context(), login(t, s, "user", fixturePassword, "second_factor"), codeAt(t, secret, clock.Now()), "Other laptop")
	if err != nil {
		t.Fatal(err)
	}
	devices, err := s.OwnDevices(t.Context(), first.AccessToken)
	if err != nil || len(devices) != 2 {
		t.Fatal(err)
	}
	current := 0
	for _, d := range devices {
		if d.Current {
			current++
			if d.ID != first.DeviceID {
				t.Fatal("incorrect current device")
			}
		}
	}
	if current != 1 {
		t.Fatal("missing current device")
	}

	actor := principalFor(t, s, "admin")
	foreign := database.DeviceSession{ID: newID(), AccountID: actor.accountID, Name: "Admin browser", Epoch: actor.epoch, ExpiresAt: clock.Now().Add(SessionLifetime)}
	if err = s.repo.CreateSession(t.Context(), &foreign, accessHash(newSecret()), clock.Now()); err != nil {
		t.Fatal(err)
	}
	if err = s.RevokeOwnDevice(t.Context(), first.AccessToken, foreign.ID, false); !errors.Is(err, ErrCredentials) {
		t.Fatal("revoked another account's device", err)
	}

	if err = s.RevokeOwnDevice(t.Context(), first.AccessToken, "", true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Authorize(t.Context(), first.AccessToken); err != nil {
		t.Fatal("other-device logout ended caller", err)
	}
	if _, err = s.Authorize(t.Context(), second.AccessToken); !errors.Is(err, ErrCredentials) {
		t.Fatal("other device still authorized")
	}
	if _, err = s.Refresh(t.Context(), rotation(second)); !errors.Is(err, ErrCredentials) {
		t.Fatal("revoked device refreshed")
	}
	if err = s.RevokeOwnDevice(t.Context(), first.AccessToken, first.DeviceID, false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.OwnDevices(t.Context(), first.AccessToken); !errors.Is(err, ErrCredentials) {
		t.Fatal("signed out caller can list devices")
	}
}

func TestOwnDevicePaginationReachesEveryRegistration(t *testing.T) {
	s, clock, caller, _ := refreshFixture(t)
	source, err := s.repo.SessionByToken(t.Context(), accessHash(caller.AccessToken), clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	for range 104 {
		device := database.DeviceSession{ID: newID(), AccountID: caller.AccountID, Name: "Laptop", Epoch: source.Epoch, ExpiresAt: clock.Now().Add(database.AccessLifetime), RefreshHash: refreshHash(newSecret())}
		if err := s.repo.CreateSession(t.Context(), &device, accessHash(newSecret()), clock.Now()); err != nil {
			t.Fatal(err)
		}
	}
	before := protocol.ID("")
	seen := map[protocol.ID]bool{}
	for {
		page, err := s.OwnDevices(t.Context(), caller.AccessToken, before)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) > 100 {
			t.Fatal("unbounded page")
		}
		for _, device := range page {
			if seen[device.ID] {
				t.Fatal("duplicate device across pages")
			}
			seen[device.ID] = true
		}
		if len(page) < 100 {
			break
		}
		before = page[len(page)-1].ID
	}
	if len(seen) != 105 {
		t.Fatal("device history truncated", len(seen))
	}
	if _, err := s.OwnDevices(t.Context(), caller.AccessToken, "invalid"); !errors.Is(err, ErrInput) {
		t.Fatal("invalid cursor accepted")
	}
}
