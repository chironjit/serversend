# Shared protocol foundation

This first implementation provides reusable wire types and validation in the existing `shared` Go module. The API and protocol packages use only the Go standard library. The separate [SQLite foundation](sqlite-foundation.md) uses its approved database driver. The server and desktop can adopt these contracts as their API integration is implemented.

## Implemented contracts

`shared/protocol` defines application protocol versions and transfer bounds. Version 1 is the current application version, independent of HTTP/2 or HTTP/3. Negotiation selects the highest explicitly shared version; it never assumes a peer offering version 2 also supports version 1. Empty, zero-valued and duplicate version advertisements are invalid. Unknown nonzero versions are retained for compatibility.

Transfer limits describe minimum/maximum chunk body bytes, maximum request body bytes, maximum encoded manifest bytes and maximum chunks per file. All limits must be positive, chunk bounds must be ordered, and a request must accommodate either a chunk or a manifest. A final chunk can be smaller than the minimum; all chunks must contain at least one byte. Empty files have zero chunks. Manifest size is positive even for an empty file, because its encoded metadata still occupies bytes.

These are operational bounds, not a cryptographic file format. They do not validate manifest contents, object checksums, total file length, final-chunk identity or ownership. The upload implementation must enforce those separately. Bounds compare signed 64-bit integers without multiplying count by size, avoiding overflow in these checks. Clients must apply their own memory/request budgets as well; a valid advertised limit is never an instruction to allocate that much memory.

`shared/api` identifies the API base path as `/api/v1` and defines capabilities:

```json
{
  "protocol_versions": [1],
  "transports": ["h2", "h3"],
  "limits": {
    "min_chunk_bytes": 4,
    "max_chunk_bytes": 8,
    "max_request_bytes": 16,
    "max_manifest_bytes": 16,
    "max_chunks": 2
  }
}
```

The example uses deliberately tiny test limits, not production defaults. A capability response must advertise at least one unique nonempty transport. Unknown transports remain available to future consumers; callers must explicitly choose a transport they implement. A capability response does not authenticate a server identity or grant file access.

Error responses use a stable envelope:

```json
{
  "error": {
    "code": "overloaded",
    "message": "Try again later",
    "request_id": "request-1",
    "retry_after_seconds": 5
  }
}
```

Codes cover invalid requests, authentication, permissions, missing resources, conflicts, capacity, overload, unavailability, unsupported versions/formats, missing sources and internal failures. Request ID and retry delay are optional. Only overload and unavailability are classified as transient; unknown future codes are preserved and are not automatically retried. A positive retry delay is valid only for transient codes. Before replay, the caller must still check operation idempotency and enforce its own retry count and maximum backoff. Public messages must be safe to display; encoding an error does not redact it automatically.

## Decoding and compatibility

Capability decoding reads at most 64 KiB plus one detection byte. Error decoding reads at most 16 KiB plus one detection byte. Oversized bodies return `ErrResponseTooLarge`. Underlying read errors, malformed JSON, incompatible JSON value types, overflowing integer fields, trailing JSON documents and invalid required values are rejected. Failed decodes return zero-valued contracts.

Decoding uses Go's `encoding/json` behaviour: unknown fields are ignored for additive compatibility, and duplicate JSON object keys are not independently rejected. Protocol-version and transport arrays have explicit duplicate checks. These helpers do not impose an HTTP timeout; the eventual API client must bound response duration as well as size.

## Checks and remaining work

Run `bash shared/scripts/check.sh` to check formatting, run `go vet` and run unit tests. This entry point disables workspace participation, implicit toolchain downloads and module downloads, and uses read-only module mode. It requires the locally installed Go version declared by `shared/go.mod` and the module's approved dependencies already in the module cache. Run `GOWORK=off GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go test -mod=readonly -race ./...` from `shared` for race checking where the platform supports it.

Tests cover negotiation and malformed advertisements, JSON compatibility and round trips, size limits, interrupted reads, retry classification, empty-file chunk counts, final chunks, invalid bound combinations and maximum representable integer comparisons.

Identity pairing, authentication/session contracts, pagination, concrete HTTP handlers/status mapping, upload lifecycle, catalog revisions, production resource defaults and private-file formats remain separate implementation work. In particular, capabilities do not yet advertise supported private formats, since no such format has been selected or implemented.
