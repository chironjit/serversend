// Package config parses server settings without accessing storage or opening listeners.
package config

import "os"

// Config contains the server's deployment settings. Empty TLS paths mean that
// TLS provisioning is incomplete, not that plaintext serving is permitted.
type Config struct {
	TCPAddress  string
	UDPAddress  string
	ConfigDir   string
	DataDir     string
	StagingDir  string
	TLSCertFile string
	TLSKeyFile  string
}

// Defaults returns the initial Unraid container settings.
func Defaults() Config {
	return Config{
		TCPAddress: ":8443",
		UDPAddress: ":8443",
		ConfigDir:  "/config",
		DataDir:    "/data",
		StagingDir: "/staging",
	}
}

// Load reads SERVER_SEND_* settings from the process environment.
func Load() (Config, error) {
	return FromEnvironment(os.LookupEnv)
}

// FromEnvironment reads settings using lookup and validates the result. Missing
// settings retain defaults; explicitly empty settings are validated as supplied.
// It performs no filesystem access, DNS lookups, or environment mutation.
func FromEnvironment(lookup func(string) (string, bool)) (Config, error) {
	c := Defaults()
	settings := []struct {
		name  string
		value *string
	}{
		{"SERVER_SEND_TCP_ADDRESS", &c.TCPAddress},
		{"SERVER_SEND_UDP_ADDRESS", &c.UDPAddress},
		{"SERVER_SEND_CONFIG_DIR", &c.ConfigDir},
		{"SERVER_SEND_DATA_DIR", &c.DataDir},
		{"SERVER_SEND_STAGING_DIR", &c.StagingDir},
		{"SERVER_SEND_TLS_CERT_FILE", &c.TLSCertFile},
		{"SERVER_SEND_TLS_KEY_FILE", &c.TLSKeyFile},
	}
	for _, setting := range settings {
		if value, ok := lookup(setting.name); ok {
			*setting.value = value
		}
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}
