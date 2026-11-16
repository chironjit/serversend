package api

import (
	"errors"
	"strings"
	"testing"
)

func TestAuthenticationRequestBoundaries(t *testing.T) {
	request, err := DecodePasswordAuthentication(strings.NewReader(`{"username":"admin","password":"a long 密碼 passphrase","future_field":true}`))
	if err != nil || request.Username != "admin" || request.Password != "a long 密碼 passphrase" {
		t.Fatal("valid authentication request rejected")
	}
	for _, body := range []string{
		`null`, `{}`, `[]`, `{"username":"admin","password":null}`, `{"username":"admin","password":""}`,
		`{"username":"admin","password":"one","PASSWORD":"two"}`, `{"username":"admin","password":"pw"} {}`,
		`{"username":"` + strings.Repeat("a", 129) + `","password":"pw"}`,
		`{"username":"admin","password":"` + strings.Repeat("p", 257) + `"}`,
	} {
		if _, err := DecodePasswordAuthentication(strings.NewReader(body)); err == nil {
			t.Fatal("malformed authentication request accepted")
		}
	}
	if _, err := DecodePasswordAuthentication(strings.NewReader(strings.Repeat(" ", int(MaxAuthenticationBytes)+1))); !errors.Is(err, ErrResponseTooLarge) {
		t.Fatal("authentication body limit ignored")
	}
	if _, err := DecodePasswordReplacement(strings.NewReader(`{"password":"new password 密碼"}`)); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`null`, `{}`, `{"password":null}`, `{"password":"first","Password":"second"}`, `{"password":"valid"}[]`} {
		if _, err := DecodePasswordReplacement(strings.NewReader(body)); err == nil {
			t.Fatal("malformed replacement request accepted")
		}
	}
}
