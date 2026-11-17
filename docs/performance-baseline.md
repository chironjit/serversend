# Standard-transfer performance baseline

Measured on 2026-11-17. This is an initial local baseline for feature 2, not acceptance of transport performance, storage durability, private mode or deployment qualification.

## Reproduce

From the repository root with Go 1.26.3 and the module cache restored:

```sh
go test ./shared/client -run '^$' -bench 'BenchmarkLocalStages|BenchmarkMetadataRequests' -benchtime=100x -count=1
go test ./apps/server/internal/server -run '^$' -bench BenchmarkStandardUpload -benchtime=20x -count=1
go test -race ./shared/client -run '^TestHTTP3' -count=1
go test ./apps/server/internal/server -run 'TestHTTP3|TestDual' -count=1
```

The standard-upload benchmark uses production authorization, SQLite, chunk-object writes and finalization, with a real TLS listener. Onboarding, fixture creation and one warm-up upload are outside the timer. It records server-handler time for upload creation, all chunk requests and finalization independently; remaining client/wire work is part of the end-to-end measurement. The client hashes and reads each source twice. The benchmark fixes advertised chunk sizes to compare 1, 4 and 8 MiB; it does not change production capability limits. Every operation publishes a distinct file/version. A single worker runs sequentially. Each transfer request asserts the requested HTTP version so that an HTTP/3 measurement cannot silently measure fallback instead.

The separate metadata benchmark is a disposable HTTP/2 JSON fixture. Its bounded 128-record batches measure possible request overhead reduction; they are not a production batching API and do not write durable metadata. The scan benchmark enumerates 1,024 directory entries in batches of 128; it does not model all security checks or the complete recursive upload scanner. SHA-256 throughput measures an in-memory buffer. There is no encryption stage while the private format remains unimplemented.

## Environment and limits of the measurement

| Property | Value |
| --- | --- |
| OS | Linux 7.3.3-200.fc44.x86_64, amd64 |
| CPU | AMD Ryzen 9 8945HX, 16 cores / 32 threads |
| Go | go1.26.3 linux/amd64; benchmark GOMAXPROCS 32 |
| Memory reported by host | 55,926 MiB RAM; 8,191 MiB swap, unused at inspection |
| Fixture and SQLite/object filesystem | `/tmp`, tmpfs; approximately 27 GB available at inspection |
| Repository filesystem | Btrfs; approximately 12 GB available at inspection |
| Network | Loopback TCP/UDP; no emulated latency, packet loss or bandwidth cap |
| Dataset | Non-sparse random 1-KiB and 8-MiB source files, 20 uploads per case |
| QUIC dependency | `github.com/quic-go/quic-go v0.65.0` |

The server and client share one benchmark process. Tmpfs does not establish power-loss persistence or Unraid disk performance. `B/op` is allocation volume, not peak resident memory. These short samples run on a shared workstation and have no statistical confidence interval. Request counts and relative overhead are more useful here than extrapolated deployment throughput. No cgroup CPU/memory ceiling was available from the inspected root paths.

## First measurement

This first measurement preceded the source-size buffer tuning described below.

| Standard upload | Chunk size | End-to-end ms/file | Useful MB/s | Allocation bytes/file |
| --- | --- | ---: | ---: | ---: |
| HTTP/2, 1 KiB | 4 MiB maximum | 1.307 | 0.78 | 4,315,730 |
| HTTP/3, 1 KiB | 4 MiB maximum | 1.439 | 0.71 | 4,364,052 |
| HTTP/2, 8 MiB | 1 MiB | 20.850 | 402.32 | 1,741,470 |
| HTTP/2, 8 MiB | 4 MiB | 18.495 | 453.56 | 4,968,314 |
| HTTP/2, 8 MiB | 8 MiB | 18.685 | 448.94 | 9,082,640 |
| HTTP/3, 8 MiB | 1 MiB | 26.979 | 310.93 | 3,132,890 |
| HTTP/3, 8 MiB | 4 MiB | 26.109 | 321.29 | 5,811,344 |
| HTTP/3, 8 MiB | 8 MiB | 26.001 | 322.62 | 10,045,174 |

| Selected server-handler stages | Create ms/file | Chunk ms/file | Finalize ms/file | Chunk requests/file |
| --- | ---: | ---: | ---: | ---: |
| HTTP/2, 1 KiB | 0.203 | 0.243 | 0.296 | 1 |
| HTTP/2, 8 MiB, 1-MiB chunks | 0.232 | 10.729 | 0.385 | 8 |
| HTTP/2, 8 MiB, 4-MiB chunks | 0.266 | 8.311 | 0.381 | 2 |
| HTTP/2, 8 MiB, 8-MiB chunks | 0.265 | 8.033 | 0.370 | 1 |
| HTTP/3, 1 KiB | 0.194 | 0.254 | 0.296 | 1 |
| HTTP/3, 8 MiB, 4-MiB chunks | 0.261 | 15.260 | 0.407 | 2 |

Every standard file currently needs one upload-create request, one finalization request and at least one chunk request when nonempty, with authentication and metadata lookup on each. The handler timing includes these production checks and object/database work; it is not isolated network latency or pure SQLite transaction time.

| Isolated local stage | Result |
| --- | ---: |
| SHA-256, 1-MiB buffer | 2,532 MB/s, zero allocations |
| SHA-256, 4-MiB buffer | 2,554 MB/s, zero allocations |
| SHA-256, 8-MiB buffer | 2,533 MB/s, zero allocations |
| Directory enumeration, 1,024 entries | 172 µs/batch; 168 ns/entry; 118,273 B/batch |

| Disposable HTTP/2 metadata fixture | µs/record | Requests/record |
| --- | ---: | ---: |
| One record, new TLS connection each time | 1,385.5 | 1 |
| One record, reused connection | 82.8 | 1 |
| 128 records, new TLS connection each time | 11.5 | 0.0078125 |
| 128 records, reused connection | 1.4 | 0.0078125 |

Request reuse and bounded batching deserve evaluation before increasing workers. The synthetic batching result does not prove that production transactions can batch at that rate. Production API batching needs ownership/idempotency/error/recovery semantics and further measurement before the storage API is frozen.

## Post-tuning measurement

The same command and 20-operation cases ran again after source-size buffer allocation and no-follow source handling were integrated. The final recorded sample also enabled the HTTP-version assertions described above. Other validation work may run on this workstation; do not interpret small timing differences between samples as statistically established improvements.

| Standard upload | Chunk size | End-to-end ms/file | Useful MB/s | Allocation bytes/file |
| --- | --- | ---: | ---: | ---: |
| HTTP/2, 1 KiB | 4 MiB maximum | 1.032 | 0.99 | 101,384 |
| HTTP/3, 1 KiB | 4 MiB maximum | 1.033 | 0.99 | 146,980 |
| HTTP/2, 8 MiB | 1 MiB | 20.945 | 400.50 | 1,807,629 |
| HTTP/2, 8 MiB | 4 MiB | 18.150 | 462.17 | 4,901,239 |
| HTTP/2, 8 MiB | 8 MiB | 18.141 | 462.40 | 9,054,602 |
| HTTP/3, 8 MiB | 1 MiB | 27.936 | 300.28 | 3,296,257 |
| HTTP/3, 8 MiB | 4 MiB | 26.375 | 318.05 | 5,906,238 |
| HTTP/3, 8 MiB | 8 MiB | 25.145 | 333.61 | 10,033,703 |

For the 1-KiB fixture, total allocation volume fell approximately 42.6-fold on HTTP/2 and 29.7-fold on HTTP/3 compared with the original fixed-buffer sample. This is an allocation result, not a peak RSS result. Eight-MiB chunks still approximately double allocation volume relative to four-MiB chunks, without a demonstrated throughput gain large enough to change defaults.

## Initial decisions and resource bounds

- The desktop and `client.New` now try HTTP/3 first on every connection establishment, as selected by the user. HTTP/2 is used only when QUIC fails. This loopback baseline showed no QUIC throughput advantage; target-network qualification remains open. `NewWithOptions` with `PreferHTTP3: false` retains HTTP/2-only measurement support.
- Keep the existing preferred 4-MiB chunk size and 1–8-MiB capability range. Doubling to 8 MiB did not materially improve this sample, and it approximately doubled allocation volume. The production client can still use the larger advertised chunk size for very large manifests.
- Allocate the upload buffer up to the selected chunk size and the source file size, retaining a one-byte minimum for empty-source change detection. The original 4-MiB allocation for a 1-KiB file motivated this tuning; the post-tuning run above records the resulting allocation reduction.
- Superseded 2026-11-17: the desktop now runs eight workers (see below). Existing scan backpressure and persistent-queue/resource scheduling work remain necessary; this benchmark does not qualify a maximum batch length or bounded transfer-history memory.
- HTTP/3 has a 16-KiB header limit, 32 concurrent incoming request streams per connection, an 8-MiB stream receive window and a 16-MiB connection receive window. Requests receive 30-second read/write deadlines, matching the TCP request-time budget. These are protocol-level limits, not an aggregate process-memory guarantee. Global/per-account connection limits, KDF budgets and target-hardware peak RSS remain open.

## Transport safety and operation

The server process opens configured TCP and UDP listeners (default port 58443) and serves the same authenticated API, SQLite state and object store on both. UDP bind or serving failure is logged and leaves TCP available for fallback; TCP failure stops the process and its QUIC listener. `Server.Serve` remains a TCP-only entry point for callers that explicitly need it. Only a dual-listener handler advertises HTTP/3. Compose examples expose UDP as well as TCP; actual Unraid/Tailscale UDP reachability still needs testing.

HTTP/3 requests have a 60-second budget through receipt of response headers; a progressing response body is not constrained by that timer. A stalled QUIC connection still has a one-minute idle timeout. Connection selection sends a credential-free `HEAD /api/v1/capabilities` only to the configured origin, with a 500-ms deadline. One connection establishment runs at a time per client. The selected multiplexed connection is reused until it closes; every replacement connection starts with QUIC again. There is no periodic upgrade attempt. Probe failures select HTTP/2 using the identical approved certificate pin. Alt-Svc cannot add a new host or port, and redirects remain disabled. Separate TCP and UDP ports require a future explicit endpoint/profile configuration; connection selection currently probes the configured HTTPS port.

The client waits for a complete QUIC handshake and the server explicitly sets `Allow0RTT: false`. Authentication and mutations never use early-data methods. This follows the library's [HTTP/3 client](https://quic-go.net/docs/http3/client/) and [server 0-RTT controls](https://quic-go.net/docs/http3/server/#0-rtt).

An HTTP/3 request failure selects HTTP/2 fallback for the replacement connection. Bodyless GET/HEAD requests can fall back immediately; a failed mutation is returned as uncertain because it may already have committed. There is no automatic mutation replay. The same client/session/upload IDs remain in use for later HTTP/2 calls. Automatic recovery of uncertain upload responses remains part of the persistent resumable queue feature. A failure after response headers while reading a body is reported through the existing transfer error path.

Tests exercise real TLS/HTTP/3 selection, concurrent blocked-UDP fallback, wrong certificate pins, redirects, no early credentials, no replay of an uncertain mutation, full standard upload/download and sign-out, TCP capability parity, QUIC-only certificate inspection, connection replacement, retained HTTP/2 selection, QUIC listener failure with continued TCP service, draining active QUIC requests, bounded response-header waits and stalled request-body deadlines.

On 2026-11-17 the source-free transport race checks passed, followed by the full server `go test -race -mod=readonly ./...` suite with the workspace dependency graph after filesystem dependency approval. Both production-upload benchmark commands above completed successfully; the final post-tuning run includes enforced transport selection. The standalone server command `GOWORK=off GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go test -mod=readonly -race ./...` also passed from `apps/server` (server package 23.101 seconds). This verifies the server module independently of workspace version selection. The owning `bash apps/server/scripts/check.sh` check also passed: administrator formatting/type/lint checks, all 26 administrator UI tests, production assets, standalone Go vet/tests and the server process build (server tests 16.849 seconds).

## Open acceptance gates

Encryption/KDF stages, private transfers, resume/reconnect, useful versus retry wire rates, per-user fairness, configurable global resource ceilings, aggregate peak RSS, 100,000-file scale, actual disk flushing, LAN, direct tailnet and relayed tailnet remain unmeasured. Native Windows/macOS transport execution and Unraid TCP/UDP operation remain unqualified. Extend the same rigs as those features arrive; do not mark feature 2 complete from these local samples.

## Storage safety follow-up (2026-11-17)

Re-ran the same 20-upload loopback benchmark after quota admission, expiry maintenance, serialized object mutation and additional directory flushes. Same machine and tmpfs limitations apply; checks/builds also ran on the host. Four-MiB chunk cases measured 438.07 MB/s over HTTP/2 and 314.52 MB/s over HTTP/3 for 8-MiB files (19.149/26.671 ms per file, 4,901,351/5,914,228 B/op). One-KiB files measured 1.168/1.277 ms per file (109,437/158,694 B/op). These are short local samples, not target-device throughput or peak RSS measurements. The preferred chunk size and single desktop worker remain unchanged. The server now serializes chunk publication and lifecycle mutations; fairness and aggregate resource ceilings still need target-deployment measurements before increasing concurrency.

## Concurrent small files on a real disk (2026-11-17)

`BenchmarkParallelSmallUploads` (1-KiB files, run with `TMPDIR` on the workstation's btrfs SSD rather than tmpfs, 300 uploads) measured about 37 ms per file with one worker and 28 ms with eight, on both HTTP/2 and HTTP/3. Per file: create ≈ 6 ms, chunk ≈ 24 ms, finalize ≈ 6 ms. Cost is dominated by flushes: one object fsync, three directory fsyncs and three serialized SQLite `synchronous=FULL` commits. Chunk receipt, hashing and object flushing now run outside the upload-service lock, so a slow client no longer blocks other uploads; only metadata recording is serialized. Dropping the object-root/incoming directory flushes (not applied; changes recorded durability policy) measured 29/25 ms. The desktop runs eight workers; further small-file gains need fewer commits per file, for example a batched create/finalize API or packed small objects.

Batched uploads (`POST /uploads/batch`, up to 256 files per request, one transaction and one concurrent flush round) measured 0.81 ms per 1-KiB file with one worker and 0.37 ms with eight on the same disk, versus 36 and 28 ms for individual uploads (2,048 files, HTTP/2 and HTTP/3 alike).

After the browsable-tree storage change (distinct file names, 2,048 files, HTTP/2, same disk): 28.6 ms per individual 1-KiB upload and 0.45 ms per batched file with eight workers. Applied storage-journal entries are forgotten inside the next transaction, and only directories that gained or lost entries are flushed.
