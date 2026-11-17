package config

import (
	"strings"
	"testing"
)

func TestDefaults(t *testing.T) {
	want := Config{MinFreeBytes: 64 << 20, TCPAddress: ":58443", UDPAddress: ":58443", ConfigDir: "/config", DataDir: "/data", StagingDir: "/staging"}
	got, err := FromEnvironment(environment(nil))
	if err != nil || got != want {
		t.Fatalf("FromEnvironment() = %#v, %v; want %#v", got, err, want)
	}
}

func TestEnvironmentOverrides(t *testing.T) {
	values := map[string]string{
		"SERVER_SEND_TCP_ADDRESS":   "127.0.0.1:443",
		"SERVER_SEND_UDP_ADDRESS":   "[::1]:9443",
		"SERVER_SEND_CONFIG_DIR":    "/mnt/appdata/config",
		"SERVER_SEND_DATA_DIR":      "/mnt/storage/data",
		"SERVER_SEND_STAGING_DIR":   "/mnt/cache/staging",
		"SERVER_SEND_TLS_CERT_FILE": "/certificates/server.crt",
		"SERVER_SEND_TLS_KEY_FILE":  "/certificates/server.key",
		"UNRELATED":                 "ignored",
	}
	want := Config{MinFreeBytes: 64 << 20,
		TCPAddress: "127.0.0.1:443", UDPAddress: "[::1]:9443",
		ConfigDir: "/mnt/appdata/config", DataDir: "/mnt/storage/data", StagingDir: "/mnt/cache/staging",
		TLSCertFile: "/certificates/server.crt", TLSKeyFile: "/certificates/server.key",
	}
	got, err := FromEnvironment(environment(values))
	if err != nil || got != want {
		t.Fatalf("FromEnvironment() = %#v, %v; want %#v", got, err, want)
	}
}

func TestInvalidEnvironmentReturnsNoPartialConfig(t *testing.T) {
	for _, name := range []string{"SERVER_SEND_TCP_ADDRESS", "SERVER_SEND_UDP_ADDRESS", "SERVER_SEND_CONFIG_DIR", "SERVER_SEND_DATA_DIR", "SERVER_SEND_STAGING_DIR"} {
		t.Run(name, func(t *testing.T) {
			got, err := FromEnvironment(environment(map[string]string{name: ""}))
			if err == nil || !strings.Contains(err.Error(), name) || got != (Config{}) {
				t.Fatalf("got %#v, %v; want empty Config and error naming %s", got, err, name)
			}
		})
	}
}

func TestExplicitEmptyTLSSettings(t *testing.T) {
	_, err := FromEnvironment(environment(map[string]string{"SERVER_SEND_TLS_CERT_FILE": "", "SERVER_SEND_TLS_KEY_FILE": ""}))
	if err != nil {
		t.Fatal(err)
	}
}

func TestLoadUsesProcessEnvironment(t *testing.T) {
	for _, name := range []string{"SERVER_SEND_TLS_CERT_FILE", "SERVER_SEND_TLS_KEY_FILE", "SERVER_SEND_TLS_IPS"} {
		t.Setenv(name, "")
	}
	for name, value := range map[string]string{
		"SERVER_SEND_TCP_ADDRESS": "127.0.0.1:8443", "SERVER_SEND_UDP_ADDRESS": ":8443",
		"SERVER_SEND_CONFIG_DIR": "/config", "SERVER_SEND_DATA_DIR": "/data", "SERVER_SEND_STAGING_DIR": "/staging",
	} {
		t.Setenv(name, value)
	}
	c, err := Load()
	if err != nil || c.TCPAddress != "127.0.0.1:8443" {
		t.Fatalf("Load() = %#v, %v", c, err)
	}
}

func environment(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) { value, ok := values[name]; return value, ok }
}

func TestStoragePolicyEnvironment(t *testing.T) {
	for _, name := range []string{"SERVER_SEND_STORAGE_GLOBAL_BYTES", "SERVER_SEND_STORAGE_USER_BYTES", "SERVER_SEND_STORAGE_MIN_FREE_BYTES"} {
		for _, bad := range []string{"", "-1", "garbage", "9223372036854775808"} {
			if _, err := FromEnvironment(environment(map[string]string{name: bad})); err == nil {
				t.Fatalf("accepted %s=%s", name, bad)
			}
		}
	}
	got, err := FromEnvironment(environment(map[string]string{"SERVER_SEND_STORAGE_GLOBAL_BYTES": "1000", "SERVER_SEND_STORAGE_USER_BYTES": "100", "SERVER_SEND_STORAGE_MIN_FREE_BYTES": "10"}))
	if err != nil || got.GlobalBytes != 1000 || got.UserBytes != 100 || got.MinFreeBytes != 10 {
		t.Fatalf("%+v %v", got, err)
	}
}
