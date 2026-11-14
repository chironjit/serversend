package config

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"
)

// Validate checks syntax and lexical storage separation. Startup must separately
// check mount identity, symlinks, permissions, free space and TLS file contents.
func (c Config) Validate() error {
	for _, address := range []struct{ name, value string }{
		{"SERVER_SEND_TCP_ADDRESS", c.TCPAddress},
		{"SERVER_SEND_UDP_ADDRESS", c.UDPAddress},
	} {
		if err := validateAddress(address.value); err != nil {
			return fmt.Errorf("%s: %w", address.name, err)
		}
	}
	dirs := []struct{ name, value string }{
		{"SERVER_SEND_CONFIG_DIR", c.ConfigDir},
		{"SERVER_SEND_DATA_DIR", c.DataDir},
		{"SERVER_SEND_STAGING_DIR", c.StagingDir},
	}
	for i, dir := range dirs {
		if err := validatePath(dir.value); err != nil {
			return fmt.Errorf("%s: %w", dir.name, err)
		}
		for _, previous := range dirs[:i] {
			if pathsOverlap(dir.value, previous.value) {
				return fmt.Errorf("%s and %s must be separate, non-nested directories", dir.name, previous.name)
			}
		}
	}
	if (c.TLSCertFile == "") != (c.TLSKeyFile == "") {
		return errors.New("SERVER_SEND_TLS_CERT_FILE and SERVER_SEND_TLS_KEY_FILE must both be set or both be empty")
	}
	if c.TLSCertFile != "" {
		for _, file := range []struct{ name, value string }{
			{"SERVER_SEND_TLS_CERT_FILE", c.TLSCertFile},
			{"SERVER_SEND_TLS_KEY_FILE", c.TLSKeyFile},
		} {
			if err := validatePath(file.value); err != nil {
				return fmt.Errorf("%s: %w", file.name, err)
			}
		}
	}
	return nil
}

func validateAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return errors.New("must be an IP:port or :port address; bracket IPv6 addresses")
	}
	if host != "" {
		if _, err := netip.ParseAddr(host); err != nil {
			return errors.New("listen host must be a numeric IPv4 or IPv6 address")
		}
	}
	if port == "" || strings.IndexFunc(port, func(r rune) bool { return r < '0' || r > '9' }) != -1 {
		return errors.New("port must be a decimal integer between 1 and 65535")
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil || n == 0 {
		return errors.New("port must be a decimal integer between 1 and 65535")
	}
	return nil
}

func validatePath(path string) error {
	if path == "" || strings.TrimSpace(path) != path || strings.ContainsRune(path, '\x00') || !filepath.IsAbs(path) {
		return errors.New("must be an absolute path without surrounding whitespace or NUL bytes")
	}
	if filepath.Clean(path) == string(filepath.Separator) {
		return errors.New("must not be the filesystem root")
	}
	return nil
}

func pathsOverlap(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	separator := string(filepath.Separator)
	return a == b || strings.HasPrefix(a, b+separator) || strings.HasPrefix(b, a+separator)
}
