package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chironjit/serversend/apps/server/internal/auth"
	"github.com/chironjit/serversend/apps/server/internal/auth/twofactor"
	"github.com/chironjit/serversend/apps/server/internal/database"
	"github.com/chironjit/serversend/shared/api"
	"github.com/pquerna/otp/totp"
)

func enrollmentHandler(t *testing.T, configured bool) (http.Handler, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	var options []auth.Option
	if configured {
		protector, err := twofactor.NewSecretProtector(bytes.Repeat([]byte{9}, 32))
		if err != nil {
			t.Fatal(err)
		}
		options = append(options, auth.WithTwoFactor("0123456789abcdef0123456789abcdef", protector))
	}
	service, err := auth.New(database.NewRepository(db), adapterTestHasher{}, nil, options...)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := service.BootstrapAdmin(t.Context(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	h := New(nil, WithAuthentication(service))
	body, _ := json.Marshal(api.PasswordAuthentication{Username: "admin", Password: issued.TemporaryPassword})
	var attempt api.PasswordAttempt
	if err := json.Unmarshal(authRequest(h, passwordPath, string(body), "").Body.Bytes(), &attempt); err != nil {
		t.Fatal(err)
	}
	if w := authRequest(h, passwordReplacePath, `{"password":"a long permanent password"}`, attempt.Challenge); w.Code != 204 {
		t.Fatal(w)
	}
	w := authRequest(h, passwordPath, `{"username":"admin","password":"a long permanent password"}`, "")
	if err := json.Unmarshal(w.Body.Bytes(), &attempt); err != nil || attempt.NextStep != "enroll_totp" {
		t.Fatal(w)
	}
	return h, attempt.Challenge
}

func TestTOTPEnrollmentHTTPFlow(t *testing.T) {
	h, challenge := enrollmentHandler(t, true)
	if w := authRequest(h, totpEnrollPath, `{}`, ""); w.Code != 401 {
		t.Fatal("enrollment without a challenge accepted")
	}
	for _, body := range []string{``, `null`, `[]`, `{}{}`} {
		if w := authRequest(h, totpEnrollPath, body, challenge); w.Code != 400 {
			t.Fatalf("malformed enrollment body %q: %d", body, w.Code)
		}
	}
	for _, body := range []string{`{}`, `{"code":""}`, `{"code":123456}`, `{"code":"` + strings.Repeat("1", 65) + `"}`} {
		if w := authRequest(h, totpConfirmPath, body, challenge); w.Code != 400 {
			t.Fatalf("malformed confirmation body %q: %d", body, w.Code)
		}
	}
	if w := authRequest(h, totpConfirmPath, `{"code":"123456"}`, challenge); w.Code != 401 {
		t.Fatal("confirmation before enrollment accepted")
	}
	w := authRequest(h, totpEnrollPath, `{}`, challenge)
	var enrollment api.TOTPEnrollment
	if err := json.Unmarshal(w.Body.Bytes(), &enrollment); err != nil || w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Set-Cookie") != "" {
		t.Fatalf("enrollment: %d %v", w.Code, err)
	}
	if len(enrollment.Secret) != 32 || !strings.HasPrefix(enrollment.URI, "otpauth://totp/") || !bytes.HasPrefix(enrollment.QRPNG, []byte("\x89PNG")) {
		t.Fatal("enrollment response is incomplete")
	}
	code, err := totp.GenerateCode(enrollment.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	w = authRequest(h, totpConfirmPath, `{"code":"`+code+`"}`, challenge)
	var codes api.RecoveryCodes
	if err := json.Unmarshal(w.Body.Bytes(), &codes); err != nil || w.Code != 200 || len(codes.Codes) != twofactor.RecoveryCodeCount || w.Header().Get("Set-Cookie") != "" {
		t.Fatalf("confirmation: %d %v", w.Code, err)
	}
	if strings.Contains(w.Body.String(), enrollment.Secret) {
		t.Fatal("confirmation echoed the TOTP secret")
	}
	// The service admits a burst of ten attempts; this flow stays within it.
	if w := authRequest(h, totpConfirmPath, `{"code":"`+code+`"}`, challenge); w.Code != 401 {
		t.Fatal("spent enrollment challenge accepted")
	}
	// Completed onboarding still yields only a restricted second-factor step.
	w = authRequest(h, passwordPath, `{"username":"admin","password":"a long permanent password"}`, "")
	var attempt api.PasswordAttempt
	if err := json.Unmarshal(w.Body.Bytes(), &attempt); err != nil || attempt.NextStep != "second_factor" {
		t.Fatal(w)
	}
	if w := authRequest(h, totpEnrollPath, `{}`, attempt.Challenge); w.Code != 401 {
		t.Fatal("login challenge replaced an active enrollment")
	}
}

func TestTOTPEnrollmentHTTPUnavailableAndMethodBoundaries(t *testing.T) {
	h, challenge := enrollmentHandler(t, false)
	w := authRequest(h, totpEnrollPath, `{}`, challenge)
	if w.Code != 503 || strings.Contains(w.Body.String(), "key") {
		t.Fatalf("unconfigured enrollment: %d %s", w.Code, w.Body)
	}
	if failure, err := api.DecodeError(w.Body); err != nil || failure.Code != api.Unavailable {
		t.Fatal("unavailable two-factor is not a stable typed error")
	}
	if w := authRequest(h, totpConfirmPath, `{"code":"123456"}`, challenge); w.Code != 503 {
		t.Fatalf("unconfigured confirmation: %d", w.Code)
	}
	for _, path := range []string{totpEnrollPath, totpConfirmPath} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != 405 || w.Header().Get("Allow") != "POST" {
			t.Fatalf("%s method boundary ignored: %d", path, w.Code)
		}
	}
}
