package api

import (
	"errors"
	"fmt"
	"io"

	"github.com/chironjit/serversend/shared/protocol"
)

const MaxCapabilitiesBytes = 64 * 1024

type Transport string

const (
	HTTP2 Transport = "h2"
	HTTP3 Transport = "h3"
)

// Capabilities advertises application protocol and transfer limits. It proves
// neither server identity nor authentication, and does not select a transport.
type Capabilities struct {
	ProtocolVersions []protocol.Version      `json:"protocol_versions"`
	Transports       []Transport             `json:"transports"`
	Limits           protocol.TransferLimits `json:"limits"`
}

func (c Capabilities) Validate() error {
	if err := protocol.ValidateVersions(c.ProtocolVersions); err != nil {
		return err
	}
	if err := c.Limits.Validate(); err != nil {
		return err
	}
	if len(c.Transports) == 0 {
		return errors.New("at least one transport is required")
	}
	seen := make(map[Transport]bool, len(c.Transports))
	for _, transport := range c.Transports {
		if transport == "" || seen[transport] {
			return fmt.Errorf("empty or duplicate transport: %q", transport)
		}
		seen[transport] = true
	}
	return nil
}

// SupportsTransport tests exact support; unknown transports may be advertised
// by future peers but must never be interpreted as one of the known transports.
func (c Capabilities) SupportsTransport(transport Transport) bool {
	for _, offered := range c.Transports {
		if offered == transport {
			return true
		}
	}
	return false
}

func DecodeCapabilities(r io.Reader) (Capabilities, error) {
	var capabilities Capabilities
	if err := decode(r, MaxCapabilitiesBytes, &capabilities); err != nil {
		return Capabilities{}, err
	}
	if err := capabilities.Validate(); err != nil {
		return Capabilities{}, err
	}
	return capabilities, nil
}
