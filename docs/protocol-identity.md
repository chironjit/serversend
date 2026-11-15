# Identity, trust and lifecycle contract

This specifies protocol v1. The pure proof and lifecycle rules live in `shared/protocol`; HTTP handlers, account repositories and secure desktop credential storage remain later implementation work. See [protocol foundation](protocol-foundation.md) for encoding and bounds.

## Server pairing and endpoint trust

A profile pins `(server_id, Ed25519 public_key)` and holds an explicit list of approved HTTPS origins. Each origin is canonical: lowercase ASCII DNS/punycode or numeric IP, optional decimal non-default port, no credentials, trailing slash, path, query, fragment or zone identifier. IPv6 is bracketed and compressed; an omitted port means 443. An origin such as `https://host.tailnet.ts.net:8443` is distinct from a LAN origin. `ValidateOrigin` checks syntax, not certificate trust or authorization to contact a host. Profiles must not probe unapproved origins.

Before any application credential is sent:

1. Establish TLS 1.3 with hostname/IP, validity and certificate-chain verification against the OS roots or an explicitly provisioned private trust anchor. A private/self-signed deployment must provision that trust first. A manually compared identity fingerprint does not waive TLS verification. Never use an insecure-verification fallback. Bind the proof to the leaf certificate from the **actual connection** used to request it.
2. Generate a fresh 32-byte cryptographically random nonce and send `POST /api/v1/identity/proof` with `IdentityChallenge`, without cookies or Authorization. The server checks the requested origin against its configured externally served origins. This endpoint is read-only despite POST; bound/rate-limit it. No redirects are followed.
3. Receive `IdentityProof` and verify the signature, connection origin, nonce, protocol, leaf certificate digest, TLS channel binding and time interval. For a new profile, trust the identity through the previously verified TLS origin, or additionally compare `SHA256(raw public key)` with a fingerprint delivered by the administrator over an independent trusted channel. A response cannot authenticate its own first-use key without one of these anchors. Record the pin only after this verification succeeds and the chosen server is accepted.
4. For existing profiles, require an exact match to the stored server ID **and** public key. Approving an additional LAN/tailnet origin requires the same checks against the existing pin. Only then may the profile send its account credentials to that origin. Serialize pairing and credential release so a route cannot change between them.

Each new TLS connection must authenticate the pinned server before credential-bearing requests use it. A client transport must gate every pooled connection (or authenticate each new connection before reuse), not perform a proof on one connection and then blindly use a different connection. Prefer HTTP/2 connection reuse. QUIC uses the same rules when implemented. Re-probe only approved routes, with bounded timeouts and recent-failure caching. A route change preserves server/account/upload identity and repeats verification before resuming missing work.

Do not follow redirects from credential-bearing requests, including same-origin redirects; return a clear endpoint-repair error instead. This avoids Authorization/cookie forwarding and removes ambiguity about the actual credential destination. No authentication or mutation uses TLS/QUIC 0-RTT. A capability response or matching server ID alone is never a trust proof.

### Signed proof

The exact JSON fixture is [identity-proof.json](../shared/api/testdata/identity-proof.json); [identity-challenge.json](../shared/api/testdata/identity-challenge.json) is its request. Their zero seed/nonce and fake certificate bytes are deterministic test material, never production randomness. `IdentityProof.SigningBytes` defines this ASCII transcript, separated by a single LF with **no final LF**:

```text
ServerSend identity proof v1
<32-character server_id>
<unpadded-base64url raw 32-byte public_key>
<unpadded-base64url raw 32-byte nonce>
<canonical HTTPS origin>
<lowercase SHA256 of the connection leaf certificate DER>
<unpadded-base64url 32-byte TLS exporter channel binding>
<canonical positive Unix issued_at seconds>
<canonical positive Unix expires_at seconds>
```

Derive `channel_binding` on both peers from the actual TLS connection with `ExportKeyingMaterial("EXPORTER-ServerSend-Identity-v1", nil, 32)`. The server derives both the exporter and leaf digest itself; it must never sign caller-supplied connection bindings. This prevents a proof from being relayed through a second TLS connection using the same certificate. TLS termination at a reverse proxy requires a separately designed authenticated binding and is not supported by this direct-TLS v1 proof flow.

The response's `protocol_version` must be 1; the fixed domain-separation line commits to that version. Sign exactly those bytes with the persisted server Ed25519 key. Encode the raw 64-byte signature as unpadded base64url. Do not sign serialized JSON or a PEM certificate. The public fingerprint is lowercase SHA-256 of the raw 32-byte Ed25519 public key, independent of server ID and TLS certificate. The ID and key are both covered by the signature.

Require `0 < issued_at < expires_at`, a lifetime of at most 120 seconds, and `issued_at - 30 <= now < expires_at`. The desktop additionally imposes a bounded request deadline, tracks outstanding nonces per origin, and consumes a nonce once a proof succeeds; never reuse it after reconnect or restart. `Verify` checks the supplied outstanding challenge and trust, signature, certificate/channel binding and clock interval. Outstanding-nonce storage, one-time consumption, TLS trust and connection gating remain the caller's responsibility. Time skew is not permission to reuse a challenge.

### Renewal, replacement and restore

Renewing a TLS certificate retains the persisted server ID/key. Verify the new certificate normally and obtain a fresh proof bound to its new leaf digest; no routine account login is required. Hostname/CA changes require explicit endpoint/trust-anchor repair, not silent acceptance. An expired certificate prevents credentials from being sent even if an identity proof would otherwise verify.

An ID or key mismatch blocks that route and suspends its jobs. Never send existing access/refresh credentials to the replacement, overwrite the pin automatically or try an unapproved address. The repair UI shows old/new public fingerprints and the TLS failure, and offers explicit re-pairing. Treat a genuinely independent server as a new profile with independent login, catalog and queue state. Do not transplant tokens, remote identifiers or vault assumptions.

A replacement restored with the same `/config` identity is the same logical server and retains device authorization if its account/session database is intact. It must not run concurrently with the original. A clone is independent and requires a fresh identity and explicit migration workflow. Losing identity material is an operations/restore problem; never silently regenerate corrupt existing identity.

## Account states and restricted access

`AccountStatus` separates onboarding state, verified TOTP enrollment, and administrative disablement:

| Current state | Action | Result |
| --- | --- | --- |
| `password_required`, no enrolled TOTP | Validate temporary credential | Restricted password-change authentication; durable state unchanged |
| `password_required`, no enrolled TOTP | Replace temporary password | `totp_required` |
| `totp_required` | Validate the permanent password on a new attempt | Restricted enrollment authentication; durable state unchanged |
| `totp_required` | Verify provisional TOTP and atomically consume its time step | `active`, `totp_enrolled=true`; issue recovery codes once |
| Any state | Administrator password reset | `password_required`; preserve enrolled TOTP; revoke all sessions |
| `password_required` with existing TOTP | Replace password | `active`; require existing TOTP/recovery proof before issuing a full session |
| Any state | Reauthenticated administrator TOTP reset | Clear TOTP/recovery codes, revoke sessions; `totp_required`, except preserve `password_required` when still required |
| Any state | Disable/enable | Change `disabled` independently; never skip onboarding |

Administrators use the same mandatory onboarding as users. Bootstrap is a local command or expiring single-use setup capability, never a default password or permanently open setup route. Temporary credentials expire, are hashed, and are consumed/replaced atomically. Provisional enrollment is not active TOTP; interrupted enrollment can resume via a fresh restricted password-authentication attempt. An accepted code time step/recovery-code hash is consumed atomically with the state/session transaction. Resetting TOTP is separate from resetting the password and requires administrator reauthentication and auditing. Neither resets nor disablement alter private vault keys.

A disabled account cannot authenticate, replace its password, finish enrollment, refresh or use an existing session. Enabling it does not resurrect revoked sessions. `AccountStatus.Apply` defines valid state changes; it does not grant authority, validate a password, prove TOTP possession or revoke sessions. In particular, `active` is account readiness, **not** evidence that the current request completed both factors.

The later authentication handlers must use these explicit authorization classes:

| Credential class | Allowed work |
| --- | --- |
| Bootstrap | Create the first administrator only; single-use, expiring |
| Password-change | Replace the password and end the restricted attempt only |
| Enrollment | Provision/verify TOTP and retrieve newly issued recovery codes once |
| Second-factor challenge | Complete TOTP/recovery verification for that login only |
| Active desktop session | Own account's catalog, files, vault envelopes and own devices |
| Active browser admin session | Role-checked administrative operations; sensitive actions also require recent reauthentication |

Restricted challenges expire after at most ten minutes and do not carry file, device-management or admin authority. Session creation always checks current account/role/onboarding state. Account IDs supplied in paths or JSON never extend a caller's authority. Role checks and foreign-owner tests belong to every handler/service.

## Device authorization and refresh recovery

A device registration belongs to exactly one server/account and one local installation identity; display names are untrusted labels. Access tokens are short-lived (v1 default ten minutes), and each authorized request checks the live session/device/account state. Desktop device authorization and the refresh family have **no routine inactivity or absolute expiry**. App/OS restart, long inactivity, offline periods and ordinary server upgrades renew silently. Only logout, explicit revocation, resets, disablement, detected compromise, lost credentials or a trust-breaking server change require interactive authentication. Secure-store lock requires local OS unlock; it is not a fresh ServerSend login.

Initial password plus TOTP/single-use recovery authentication creates a distinct device registration and a random 32-byte refresh credential. Store only its hash on the server; keep the usable value in the OS credential store under server/account/device identity. Never fall back to plaintext. Access tokens live in process memory. Serialize refresh per account across all workers. Refresh is permitted only on a verified, pinned connection and is never a 0-RTT or redirectable operation.

For interruption-safe rotation, use client-persisted successor credentials rather than depending on receipt of a newly generated server secret:

1. Before sending refresh, generate a random 32-byte successor and a random 128-bit `rotation_id`. Atomically persist a pending record containing old credential, successor and ID in the secure store. If this fails, do not send the request.
2. `POST /api/v1/sessions/refresh` sends `refresh_credential`, `next_refresh_credential` (canonical unpadded base64url 32-byte secrets) and `rotation_id` (opaque ID). In one transaction the server checks active authorization, consumes the current hash, and records the successor hash plus the previous hash/rotation tuple. It returns a short-lived `access_token`, `expires_at`, `device_id` and `session_id`; it never needs to store or return the usable successor.
3. An exact replay of that pending tuple is accepted only if the provided successor hashes to the **current** credential and the previous hash/rotation ID also match. This proves possession of the successor; possession of a spent credential alone cannot recover an access token. Return fresh access authorization for the same session, without rotating again.
4. Only after a successful response does the client atomically promote the successor and clear the old pending record. If the request/response is lost, restart retries the persisted tuple, even after a long offline period. There is no short grace timeout that would force ordinary interrupted-refresh users to log in again. Worker serialization prevents a later rotation overtaking an unresolved one.
5. Reuse of the immediately previous credential with a different tuple revokes that device's refresh family as suspected compromise. Retain only the current and previous tuple for bounded state; older unrecognized credentials fail authentication without disclosing which family they belonged to. A pending exact replay after a later successful rotation is no longer recoverable. It cannot occur in a conforming serialized client unless its local secure state was rolled back; require sign-in in that case.

Use constant-time hash comparisons. Bind every tuple to the device/session/account, never to a device name. Server storage and logs contain no usable refresh credentials. Cancellation/revocation is checked before returning an idempotent result. Explicit logout revokes authorization first when online and deletes local credentials/checkpoints; offline logout deletes local credentials and warns that server revocation requires another authorized device/admin. It does not silently resurrect the session at reconnect.

Browser admin sessions use Secure, HttpOnly, SameSite=Strict cookies, validated same-origin mutations and CSRF tokens, with a default 30-minute idle/eight-hour absolute expiry and reauthentication within five minutes for resets and other sensitive actions. These browser expiry rules do not apply to desktop device authorization. Runtime authentication chunks implement and test these defaults before exposure.

Revocation rejects new requests immediately after the transaction and cancels active streams within a target maximum of five seconds, tested with a fake clock/cancellation hooks in Chunk 07. Events may accelerate UI updates but are not the revocation mechanism. Revoking one device must leave other devices usable unless an explicit account-wide reset/disable action applies. Revocation cannot erase data or vault keys already downloaded.
