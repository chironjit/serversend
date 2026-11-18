package client

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/chironjit/serversend/shared/protocol"
)

func TestApprovedRoutesValidationAndFailureCache(t *testing.T) {
	origin := "https://127.0.0.1:58443"
	trust := Trust{Identity: protocol.TrustedIdentity{ServerID: "11111111111111111111111111111111", PublicKey: base64.RawURLEncoding.EncodeToString(make([]byte, 32))}}
	for _, endpoints := range [][]string{{origin}, {"http://127.0.0.2:80"}, {"https://127.0.0.2:58443/path"}, {"https://127.0.0.2:58443", "https://127.0.0.2:58443"}, make([]string, 8)} {
		if c, err := NewVerifiedRoutes(origin, endpoints, trust); err == nil {
			c.Close()
			t.Fatal("invalid approved endpoints accepted")
		}
	}
	if c, err := NewVerifiedRoutes(origin, []string{"https://127.0.0.2:58443"}, Trust{}); err == nil {
		c.Close()
		t.Fatal("alternate route accepted before pairing")
	}
	c, err := NewVerifiedRoutes(origin, []string{"https://127.0.0.2:58443"}, trust)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	routes := c.http.Transport.(*routeTransport)
	request, _ := http.NewRequestWithContext(t.Context(), "POST", "https://unapproved.test:58443/auth/password", nil)
	if _, err = routes.RoundTrip(request); err == nil {
		t.Fatal("unapproved origin reached transport")
	}
	for i := range routes.failed {
		routes.failed[i] = time.Now().Add(time.Minute)
	}
	if _, err = c.Password(t.Context(), "user", "never-send"); err == nil || !strings.Contains(err.Error(), "temporarily unavailable") {
		t.Fatal("cached failed routes probed", err)
	}
}
