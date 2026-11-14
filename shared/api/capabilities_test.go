package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/chironjit/serversend/shared/protocol"
)

const capabilitiesJSON = `{"protocol_versions":[1],"transports":["h2","h3"],"limits":{"min_chunk_bytes":4,"max_chunk_bytes":8,"max_request_bytes":16,"max_manifest_bytes":16,"max_chunks":2}}`

func TestCapabilitiesRoundTrip(t *testing.T) {
	got, err := DecodeCapabilities(strings.NewReader(capabilitiesJSON))
	if err != nil {
		t.Fatal(err)
	}
	want := Capabilities{[]protocol.Version{1}, []Transport{HTTP2, HTTP3}, protocol.TransferLimits{MinChunkBytes: 4, MaxChunkBytes: 8, MaxRequestBytes: 16, MaxManifestBytes: 16, MaxChunks: 2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != capabilitiesJSON {
		t.Fatalf("wire contract changed: %s", encoded)
	}
	for _, transport := range []Transport{HTTP2, HTTP3} {
		if !got.SupportsTransport(transport) {
			t.Fatalf("missing transport %q", transport)
		}
	}
	if got.SupportsTransport("http1") {
		t.Fatal("unadvertised transport accepted")
	}
}

func TestDecodeCapabilitiesInvalid(t *testing.T) {
	tests := map[string]string{
		"empty": "", "null": "null", "missing fields": `{}`, "array": `[]`,
		"broken JSON": `{`, "trailing document": capabilitiesJSON + `{}`,
		"no versions":         strings.Replace(capabilitiesJSON, `[1]`, `[]`, 1),
		"zero version":        strings.Replace(capabilitiesJSON, `[1]`, `[0]`, 1),
		"negative version":    strings.Replace(capabilitiesJSON, `[1]`, `[-1]`, 1),
		"overflow version":    strings.Replace(capabilitiesJSON, `[1]`, `[65536]`, 1),
		"fractional version":  strings.Replace(capabilitiesJSON, `[1]`, `[1.5]`, 1),
		"duplicate versions":  strings.Replace(capabilitiesJSON, `[1]`, `[1,1]`, 1),
		"no transports":       strings.Replace(capabilitiesJSON, `["h2","h3"]`, `[]`, 1),
		"empty transport":     strings.Replace(capabilitiesJSON, `"h2"`, `""`, 1),
		"duplicate transport": strings.Replace(capabilitiesJSON, `"h3"`, `"h2"`, 1),
		"invalid limits":      strings.Replace(capabilitiesJSON, `"max_chunks":2`, `"max_chunks":0`, 1),
		"overflow size":       strings.Replace(capabilitiesJSON, `"max_chunks":2`, `"max_chunks":9223372036854775808`, 1),
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := DecodeCapabilities(strings.NewReader(body))
			if err == nil || !reflect.DeepEqual(got, Capabilities{}) {
				t.Fatalf("got (%#v, %v), want zero value and error", got, err)
			}
		})
	}
}

func TestDecodeCapabilitiesAdditiveCompatibility(t *testing.T) {
	body := strings.Replace(capabilitiesJSON, `[1]`, `[1,2]`, 1)
	body = strings.Replace(body, `"h3"`, `"future"`, 1)
	body = strings.Replace(body, `"limits":`, `"future_field":{"enabled":true},"limits":`, 1)
	got, err := DecodeCapabilities(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if got.SupportsTransport(HTTP3) || !got.SupportsTransport(HTTP2) {
		t.Fatal("unknown transport confused with a supported transport")
	}
	if selected, err := protocol.NegotiateVersion([]protocol.Version{1}, got.ProtocolVersions); selected != 1 || err != nil {
		t.Fatalf("selection = %v, %v", selected, err)
	}
}

func TestDecodeCapabilitiesSizeBound(t *testing.T) {
	body := capabilitiesJSON + strings.Repeat(" ", MaxCapabilitiesBytes-len(capabilitiesJSON))
	if _, err := DecodeCapabilities(strings.NewReader(body)); err != nil {
		t.Fatalf("exact bound rejected: %v", err)
	}
	reader := bytes.NewReader([]byte(body + "  more unread data"))
	if _, err := DecodeCapabilities(reader); !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("got %v, want ErrResponseTooLarge", err)
	}
	if reader.Len() == 0 {
		t.Fatal("read the entire oversized response")
	}
}

type failedReader struct{}

func (failedReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestDecodeCapabilitiesReadFailure(t *testing.T) {
	if _, err := DecodeCapabilities(failedReader{}); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("got %v, want read failure", err)
	}
}
