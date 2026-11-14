package config

import "testing"

func TestListenAddresses(t *testing.T) {
	tests := []struct {
		address string
		valid   bool
	}{
		{":1", true}, {":65535", true}, {"0.0.0.0:8443", true},
		{"127.0.0.1:8443", true}, {"[::]:8443", true}, {"[fe80::1%eth0]:8443", true},
		{"", false}, {"8443", false}, {"::1:8443", false}, {"[::1]", false},
		{":0", false}, {":65536", false}, {":9999999999999999999999", false},
		{":-1", false}, {":+1", false}, {":https", false}, {":", false},
		{":8443 ", false}, {" :8443", false}, {":８４４３", false},
		{"localhost:8443", false}, {"not an address:8443", false}, {"999.0.0.1:8443", false},
	}
	for _, tt := range tests {
		t.Run(tt.address, func(t *testing.T) {
			for _, tcp := range []bool{true, false} {
				c := Defaults()
				if tcp {
					c.TCPAddress = tt.address
				} else {
					c.UDPAddress = tt.address
				}
				if err := c.Validate(); (err == nil) != tt.valid {
					t.Fatalf("Validate() = %v, want valid=%v (TCP=%v)", err, tt.valid, tcp)
				}
			}
		})
	}
}

func TestStoragePaths(t *testing.T) {
	tests := []struct {
		name, config, data, staging string
		valid                       bool
	}{
		{"separate", "/config", "/data", "/staging", true},
		{"similar prefix", "/app/config", "/app/data", "/app/data-staging", true},
		{"spaces inside", "/app/config files", "/data", "/staging", true},
		{"empty", "", "/data", "/staging", false},
		{"relative", "/config", "data", "/staging", false},
		{"root", "/config", "/data", "/", false},
		{"cleaned root", "/config", "/data/..", "/staging", false},
		{"whitespace", " /config", "/data", "/staging", false},
		{"nul", "/config", "/da\x00ta", "/staging", false},
		{"same", "/config", "/data", "/data", false},
		{"alias", "/config", "/data", "/tmp/../data/", false},
		{"nested staging", "/config", "/data", "/data/incomplete", false},
		{"nested data", "/config", "/staging/data", "/staging", false},
		{"nested config", "/data/config", "/data", "/staging", false},
		{"config equals staging", "/config", "/data", "/config", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Defaults()
			c.ConfigDir, c.DataDir, c.StagingDir = tt.config, tt.data, tt.staging
			if err := c.Validate(); (err == nil) != tt.valid {
				t.Fatalf("Validate() = %v, want valid=%v", err, tt.valid)
			}
		})
	}
}

func TestTLSPaths(t *testing.T) {
	tests := []struct {
		name, cert, key string
		valid           bool
	}{
		{"unconfigured", "", "", true},
		{"configured", "/config/tls/server.crt", "/config/tls/server.key", true},
		{"missing key", "/config/server.crt", "", false},
		{"missing cert", "", "/config/server.key", false},
		{"relative cert", "server.crt", "/config/server.key", false},
		{"relative key", "/config/server.crt", "server.key", false},
		{"combined PEM file", "/config/server.pem", "/config/server.pem", true},
		{"aliased combined PEM file", "/config/server.pem", "/config/tls/../server.pem", true},
		{"root file", "/", "/config/server.key", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Defaults()
			c.TLSCertFile, c.TLSKeyFile = tt.cert, tt.key
			if err := c.Validate(); (err == nil) != tt.valid {
				t.Fatalf("Validate() = %v, want valid=%v", err, tt.valid)
			}
		})
	}
}
