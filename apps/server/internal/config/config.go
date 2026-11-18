// Package config parses server settings without accessing storage or opening listeners.
package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config contains the server's deployment settings. Empty TLS paths mean that
// TLS provisioning is incomplete unless TLSIPs selects private IP certificates.
// Plaintext serving is never permitted.
type Config struct {
	GlobalBytes, UserBytes, MinFreeBytes int64
	TCPAddress                           string
	UDPAddress                           string
	ConfigDir                            string
	DataDir                              string
	StagingDir                           string
	TLSCertFile                          string
	TLSKeyFile                           string
	TLSIPs                               string
	// UpgradeBackupDir receives an automatic stopped-server backup before a
	// schema migration. "none" deliberately skips it; empty refuses upgrades.
	UpgradeBackupDir string
}

// SkipUpgradeBackup is the explicit operator opt-out for UpgradeBackupDir.
const SkipUpgradeBackup = "none"

// Defaults returns the initial Unraid container settings.
func Defaults() Config {
	return Config{
		MinFreeBytes: 64 << 20,
		TCPAddress:   ":58443",
		UDPAddress:   ":58443",
		ConfigDir:    "/config",
		DataDir:      "/data",
		StagingDir:   "/staging",
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
		{"SERVER_SEND_TLS_IPS", &c.TLSIPs},
		{"SERVER_SEND_UPGRADE_BACKUP_DIR", &c.UpgradeBackupDir},
	}
	for _, setting := range settings {
		if value, ok := lookup(setting.name); ok {
			*setting.value = value
		}
	}
	for _, setting := range []struct {
		name  string
		value *int64
	}{
		{"SERVER_SEND_STORAGE_GLOBAL_BYTES", &c.GlobalBytes},
		{"SERVER_SEND_STORAGE_USER_BYTES", &c.UserBytes},
		{"SERVER_SEND_STORAGE_MIN_FREE_BYTES", &c.MinFreeBytes},
	} {
		if raw, ok := lookup(setting.name); ok {
			value, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || value < 0 {
				return Config{}, fmt.Errorf("%s must be a non-negative byte count", setting.name)
			}
			*setting.value = value
		}
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}
