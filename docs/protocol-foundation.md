# Protocol v1 contracts

Chunk 02 defines the wire contract in `shared/api` and transport-independent rules in `shared/protocol`. These packages use the Go standard library. This is a contract implementation: account, pairing, catalog and transfer handlers and persistent workflows are delivered by their later chunks. The running server currently exposes only the process endpoints listed in [server development](server-development.md).

Read [identity and sessions](protocol-identity.md) and [transfers and catalogs](protocol-transfers.md) alongside this document. [Requirement verification](requirements-verification.md) maps every section of the implementation plan to existing checks and future acceptance work. Exact round-trip examples are committed in [shared/api/testdata](../shared/api/testdata); `test-only` is exclusively a fixture format and must never be advertised by a deployment.

## Versioning and negotiation

Discover `GET /api/v1/capabilities` over an authenticated TLS connection before sending credentials or transfer requests. The URL selects application protocol v1; it is independent of HTTP/1.1, HTTP/2 and HTTP/3. Choose the highest explicitly shared `protocol_versions` value and its matching API base path; do not infer support for lower versions. No common version means stop with `unsupported_version`, retaining queued jobs until a compatible client/server is available. Never probe an untrusted origin with credentials. Version negotiation cannot downgrade TLS or identity checks.

A v1 advertisement can include:

```json
{"protocol_versions":[1],"transports":["h2"],"limits":{"min_chunk_bytes":1048576,"max_chunk_bytes":8388608,"max_request_bytes":8388608,"max_manifest_bytes":1048576,"max_chunks":1048576},"manifest_versions":[1]}
```

Limits are illustrative, provisional ceilings, not measured production defaults or buffer-allocation instructions. The current process does not advertise `manifest_versions` because it cannot accept files yet. An absent/empty `manifest_versions` or `private_formats` grants no format support. Private transfers require both an explicitly shared manifest schema and an explicitly shared private format. Unknown nonzero versions and transport/format tokens remain available for future consumers, but the client must explicitly implement its choice. Advertisements must contain nonempty, unique protocol versions and transports. Manifest versions are nonzero and unique; private format IDs are unique ASCII lowercase letter/digit/underscore/hyphen tokens of at most 64 bytes.

Adding optional object fields or new error/event codes is compatible. Changing required fields, units, identifier rules, signature transcripts, or existing state meanings requires a new application protocol. Unknown storage modes, states, entry kinds and manifest schemas are rejected. New private crypto formats require their own negotiated format ID and Chunk 17 review.

## Encoding and bounds

Control requests/responses use UTF-8 JSON objects and `application/json`. Producers use exact snake_case field names. Consumers ignore unknown additive fields but validate all known values; they persist normalized contracts rather than arbitrary raw request bodies. Duplicate member names (including names equivalent under Go's case folding), invalid UTF-8, nesting beyond 64 levels, malformed JSON, wrong value types, numeric overflow and trailing documents are rejected. Omitted/null scalar members take Go zero defaults before validation: zero indices/counts/byte lengths and false booleans are valid defaults, while empty IDs, states and revision strings fail validation. Producers emit every non-optional field; consumers must apply these same defaults. Required result/manifest arrays must use `[]` for empty values; absent/null arrays are invalid. Optional capability arrays may be absent/null and grant no support. Do not rely on JSON field order except for the separately specified identity signing transcript.

`DecodeCapabilities` reads at most 64 KiB plus one byte; `DecodeError` and individual events, identity proofs and chunk acknowledgements at most 16 KiB plus one byte; other control objects/pages at most 1 MiB plus one byte. `DecodeManifest` uses the lesser of the advertised manifest ceiling and the local 1 MiB control ceiling. An implementation that needs larger manifests must explicitly revise and test this local budget; advertising larger peer limits does not bypass it. These bounds are not release-scale qualification. The provisional 1 MiB manifest ceiling needs measurement/revision before the above-100-GiB release gate. Request handlers enforce body limits before parsing, timeouts, worker limits and account/global budgets independently. Typed helpers do not implement timeouts or authorize requests.

Counts, indices and transmitted byte lengths are nonnegative signed 64-bit JSON integers, never fractions. Consumers must parse these integers losslessly (for example, in the desktop Go layer); JavaScript must not round them through `Number`. Limits are compared without overflowing count-times-size multiplication. Revisions and event sequences are canonical unsigned 64-bit decimal **strings**, allowing exact handling by JavaScript. `"0"` means an absent file head or the initial catalog/event position; `"01"`, signs, whitespace and exponent notation are invalid. Exhausting the revision space must fail before mutation, never wrap.

Opaque resource IDs and idempotency keys are 128 random bits encoded as exactly 32 lowercase hexadecimal characters. The existing persisted server ID already uses this encoding. IDs are not capabilities and need not be unique across servers or identifier namespaces. Persist/cache remote resources and credentials under `(server_id, account_id, resource_id)`; devices and sessions are likewise server/account-scoped. The authenticated account determines ownership; a caller-supplied scope can only be checked against it, never replace it. Foreign-account resource lookups return the same `not_found` result as missing resources, after authentication, without disclosing their existence.

SHA-256 digests use 64 lowercase hexadecimal characters and describe transmitted bytes. A private digest never exposes an original plaintext hash. Opaque private payload bytes use standard padded JSON base64. Identity keys, nonces and signatures instead use canonical **unpadded base64url**, as specified in the identity document. Producers emit RFC3339 UTC timestamps; decoders also accept RFC3339 numeric offsets. Signed identity times use Unix seconds.

## Pagination

All list endpoints use `limit` (1–1000, default 100 when absent) and optional `cursor`. Invalid or duplicate query parameters are rejected, never silently clamped. `PageRequest.Validate` expects the adapter to have applied the default already. A cursor is an opaque, canonical unpadded base64url string of at most 2048 encoded bytes; clients must never inspect or manufacture one.

The first request establishes a stable snapshot; every response contains `snapshot` and optionally `next_cursor`. A missing cursor ends the list. Sort order is documented per endpoint, never offset-based. A cursor authenticates the account, server instance, query/filter, effective limit, snapshot, last sort key and expiry, using server-side state or a server-only MAC. Syntax validation alone does not provide this protection. A cursor cannot be replayed for another account, endpoint, directory, page size or server. A syntactically invalid/mismatched cursor returns `invalid_request`; an otherwise valid expired/pruned snapshot returns `snapshot_expired` (410). The client discards that partial snapshot and starts again. Revocation and account authorization are checked on every page, even for an otherwise valid cursor. Changes after snapshot creation cannot silently appear partway through a listing.

Responses must fit both the requested item count and the local control-body ceiling; a page may contain fewer items than requested. Never produce an empty page with a continuation cursor. A single entry that cannot fit must produce `request_too_large`, not an unbounded response. Cursor protection, snapshot storage/expiry and concurrent mutation tests belong to the owning catalog/upload services, not these syntax-only types.

## Typed errors and retry

An error body is `{"error":{"code":"conflict","message":"This item changed; refresh before trying again","request_id":"opaque-correlation-id"}}`. `message` is safe user-facing text, never an exception, filename from a private transfer, credential, key or internal path. `request_id` is optional; never log raw request/response bodies from credential or private routes.

| Code | HTTP status | Client action |
| --- | --- | --- |
| `invalid_request`, `unsupported_version`, `unsupported_format` | 400 | Correct the input or update to a compatible client/server; no automatic replay |
| `authentication_required` | 401 | Serialize silent refresh once; wait for sign-in only if device authorization is lost |
| `permission_denied` | 403 | Stop; account/role authorization is required |
| `not_found` | 404 | Reconcile; missing and foreign resources are indistinguishable |
| `conflict` | 409 | Fetch authoritative state; do not blindly overwrite |
| `snapshot_expired` | 410 | Restart the paginated snapshot or full catalog refresh |
| `upload_gone` | 410 | Reconcile the source and create a new upload generation |
| `request_too_large` | 413 | Reduce page/manifest size within the negotiated contract |
| `source_missing` | 422 | Local queue action: reselect/restore the source; no blind network retry |
| `overloaded` | 429 | Bounded backoff for replay-safe work |
| `unavailable` | 503 | Bounded backoff for replay-safe work |
| `capacity_exhausted` | 507 | Wait for capacity or user action |
| `internal_error`, unknown code | 500 by default | Stop automatic replay; preserve safe diagnostics |

`ErrorCode.HTTPStatus()` gives the default mapping. HTTP method rejection may use `invalid_request` with HTTP 405 and an `Allow` header. `retry_after_seconds` is optional, nonnegative, and positive only for `overloaded`/`unavailable`; handlers also emit the matching decimal HTTP `Retry-After` value. Only those two codes are `Retryable()`. Unknown error codes survive decoding but fail closed for automatic retry. The caller must still check operation idempotency, distinguish credential attempts from transfers, and cap retry count and delay. Authentication throttling must not cause a client to resubmit password/TOTP attempts automatically.

## Checks

`make check-shared` checks formatting, vet and unit/fixture tests with offline, read-only dependencies. Its real TLS contract test requires permission to bind loopback sockets; checks fail rather than skip it in a restricted sandbox. From `shared`, run `GOWORK=off GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go test -mod=readonly -race ./...` for race detection. `FuzzContractDecoders` provides seeds for manifest, upload, catalog, proof and event parsers; bounded fuzz runs are supplemental checks, not exhaustive parser verification.

Chunk 02 acceptance covers serialization/compatibility fixtures, malformed/oversized inputs, unsupported versions/formats, state-transition rules, colliding server/account IDs, signed identity boundaries and stable error decoding. Durable acknowledgements, authentication enforcement, protected cursors, credential stores, actual TLS pairing and private cryptography still require the runtime acceptance tests identified in the verification map.
