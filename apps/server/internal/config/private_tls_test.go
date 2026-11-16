package config

import (
	"strings"
	"testing"
)

func TestPrivateIPv4TLSConfiguration(t *testing.T) {
	c, err := FromEnvironment(environment(map[string]string{"SERVER_SEND_TLS_IPS": "192.168.1.20,10.10.10.50,127.0.0.1"}))
	if err != nil {
		t.Fatal(err)
	}
	ips, err := c.PrivateTLSAddresses()
	if err != nil || len(ips) != 3 {
		t.Fatalf("addresses=%v error=%v", ips, err)
	}
	for _, value := range []string{"localhost", "192.168.001.20", "192.168.1.20:8443", "https://192.168.1.20", "0.0.0.0", "224.0.0.1", "255.255.255.255", "::1", "::ffff:192.168.1.20", "192.168.1.20,", "192.168.1.20,192.168.1.20", " 192.168.1.20", "192.168.1.20, 10.10.10.50", strings.Repeat("127.0.0.1,", 17)} {
		if _, err := FromEnvironment(environment(map[string]string{"SERVER_SEND_TLS_IPS": value})); err == nil || !strings.Contains(err.Error(), "SERVER_SEND_TLS_IPS") {
			t.Fatalf("invalid IP config accepted: %q, error=%v", value, err)
		}
	}
	if _, err := FromEnvironment(environment(map[string]string{"SERVER_SEND_TLS_IPS": "127.0.0.1", "SERVER_SEND_TLS_CERT_FILE": "/cert.pem", "SERVER_SEND_TLS_KEY_FILE": "/key.pem"})); err == nil {
		t.Fatal("conflicting certificate modes accepted")
	}
}
