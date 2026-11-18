package httpapi

import (
	"github.com/chironjit/serversend/shared/api"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRetentionAndTrashAuthorization(t *testing.T) {
	h, _ := visibilityFixture(t)
	for _, tc := range []struct {
		method, path, role, body string
		want                     int
	}{
		{"GET", "/admin/retention", "", "", 401}, {"GET", "/admin/retention", "user", "", 403},
		{"GET", "/admin/retention", "admin", "", 200}, {"PUT", "/admin/retention", "admin", `{"trash_days":0,"version_days":0}`, 400},
		{"PUT", "/admin/retention", "admin", `{"trash_days":7,"version_days":90}`, 200},
		{"GET", "/trash", "admin", "", 403}, {"GET", "/trash", "user", "", 200},
		{"DELETE", "/trash/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "user", "", 400},
	} {
		r := httptest.NewRequest(tc.method, api.BasePath+tc.path, strings.NewReader(tc.body))
		if tc.role != "" {
			r.Header.Set("Authorization", "Bearer "+logTestToken(tc.role))
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}
