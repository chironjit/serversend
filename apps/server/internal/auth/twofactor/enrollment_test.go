package twofactor

import (
	"bytes"
	"encoding/base32"
	"errors"
	"image/png"
	"io"
	"net/url"
	"strings"
	"testing"
)

func TestEnrollment(t *testing.T) {
	seen := make(map[string]bool)
	for _, size := range []int{MinQRSize, MaxQRSize} {
		enrollment, err := Enroll("alice@example.com on home", size)
		if err != nil {
			t.Fatal(err)
		}
		secret, err := base32.StdEncoding.DecodeString(enrollment.Secret)
		if err != nil || len(secret) != 20 {
			t.Fatal("invalid enrollment secret")
		}
		if seen[enrollment.Secret] {
			t.Fatal("duplicate random secret")
		}
		seen[enrollment.Secret] = true
		u, err := url.Parse(enrollment.URI)
		if err != nil {
			t.Fatal("invalid provisioning URI")
		}
		if u.Scheme != "otpauth" || u.Host != "totp" || u.Path != "/ServerSend:alice@example.com on home" {
			t.Fatal("unexpected provisioning label")
		}
		for name, want := range map[string]string{"issuer": "ServerSend", "secret": enrollment.Secret, "period": "30", "digits": "6", "algorithm": "SHA1"} {
			if u.Query().Get(name) != want {
				t.Fatalf("wrong provisioning parameter %s", name)
			}
		}
		img, err := png.Decode(bytes.NewReader(enrollment.QRPNG))
		if err != nil {
			t.Fatal(err)
		}
		if img.Bounds().Dx() != size || img.Bounds().Dy() != size {
			t.Fatal("unexpected QR dimensions")
		}
	}
}

func TestEnrollmentAccountLabels(t *testing.T) {
	for _, label := range []string{"a", "alice & bob/home", "alice+tag@example.com", "名前", strings.Repeat("a", 128)} {
		t.Run(label, func(t *testing.T) {
			e, err := Enroll(label, MinQRSize)
			if err != nil {
				t.Fatal(err)
			}
			u, err := url.Parse(e.URI)
			if err != nil || u.Path != "/ServerSend:"+label {
				t.Fatal("label did not round trip")
			}
		})
	}
	for _, label := range []string{"", " ", " alice", "alice ", "alice:home", "alice\n", "ali\x00ce", "\xff", strings.Repeat("a", 129)} {
		_, err := Enroll(label, MinQRSize)
		if !errors.Is(err, ErrInvalidAccount) {
			t.Fatal("expected invalid account error")
		}
	}
}

func TestEnrollmentQRBounds(t *testing.T) {
	for _, size := range []int{-1, 0, MinQRSize - 1, MaxQRSize + 1} {
		_, err := Enroll("alice", size)
		if !errors.Is(err, ErrInvalidQRSize) {
			t.Fatalf("size %d: got %v", size, err)
		}
	}
}

func TestEnrollmentRandomFailure(t *testing.T) {
	for _, r := range []io.Reader{bytes.NewReader(nil), bytes.NewReader([]byte("short"))} {
		got, err := enroll("alice", MinQRSize, r)
		if !errors.Is(err, ErrEnrollment) || got.Secret != "" || got.URI != "" || len(got.QRPNG) != 0 {
			t.Fatal("random failure must not return partial enrollment")
		}
	}
}
