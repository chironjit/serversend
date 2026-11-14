// Package protocol defines transport-independent ServerSend wire rules.
package protocol

import (
	"errors"
	"fmt"
)

// Version identifies an application protocol, independently of HTTP version.
type Version uint16

const Version1 Version = 1

var ErrUnsupportedVersion = errors.New("no common protocol version")

// ValidateVersions rejects ambiguous or empty advertisements. Unknown nonzero
// versions remain valid so an older client can negotiate with a newer server.
func ValidateVersions(versions []Version) error {
	if len(versions) == 0 {
		return errors.New("at least one protocol version is required")
	}
	seen := make(map[Version]bool, len(versions))
	for _, version := range versions {
		if version == 0 || seen[version] {
			return fmt.Errorf("invalid or duplicate protocol version: %d", version)
		}
		seen[version] = true
	}
	return nil
}

// NegotiateVersion returns the highest version explicitly supported by both
// peers. Numeric ordering never implies support for an unlisted version.
func NegotiateVersion(local, remote []Version) (Version, error) {
	if err := ValidateVersions(local); err != nil {
		return 0, fmt.Errorf("local versions: %w", err)
	}
	if err := ValidateVersions(remote); err != nil {
		return 0, fmt.Errorf("remote versions: %w", err)
	}
	var selected Version
	supported := make(map[Version]bool, len(local))
	for _, version := range local {
		supported[version] = true
	}
	for _, version := range remote {
		if supported[version] && version > selected {
			selected = version
		}
	}
	if selected == 0 {
		return 0, ErrUnsupportedVersion
	}
	return selected, nil
}
