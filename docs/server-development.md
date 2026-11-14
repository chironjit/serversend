# Server development

The server module remains at `apps/server`. Its initial implementation is a standard-library-only configuration package under `internal/config`; there is no executable server, listener, storage engine, or authentication service yet.

Run the component checks from the repository root:

```sh
bash apps/server/scripts/check.sh
```

The script checks Go formatting, runs `go vet`, and runs the tests. It disables toolchain/module downloads and uses read-only module mode. To additionally check data races, run `GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go test -mod=readonly -race ./...` from `apps/server` with a supported Go toolchain and C compiler. No new module is required.

## Environment settings

`config.Load` reads process environment settings. `config.FromEnvironment` accepts a lookup function for isolated testing or composition. An unset variable keeps its default; an explicitly empty value is rejected except when both TLS paths are empty. Unknown variables are ignored.

| Variable | Default | Validation |
| --- | --- | --- |
| `SERVER_SEND_TCP_ADDRESS` | `:8443` | Numeric IPv4/IPv6 or empty host, decimal port 1–65535 |
| `SERVER_SEND_UDP_ADDRESS` | `:8443` | Same syntax; may use the same port as TCP |
| `SERVER_SEND_CONFIG_DIR` | `/config` | Absolute non-root path |
| `SERVER_SEND_DATA_DIR` | `/data` | Absolute non-root path |
| `SERVER_SEND_STAGING_DIR` | `/staging` | Absolute non-root path |
| `SERVER_SEND_TLS_CERT_FILE` | Empty | Absolute certificate path when provisioned |
| `SERVER_SEND_TLS_KEY_FILE` | Empty | Absolute key path when provisioned |

IPv6 listen addresses use brackets, such as `[::1]:8443`. Hostnames are deliberately unsupported for bind addresses; use an explicit local IP or an empty host to bind all interfaces. These are server bind settings, not client connection URLs.

Storage directories must be distinct and not lexically nested after path cleaning. Shared parent directories and similar names are permitted. The certificate and key paths must either both be supplied or both be empty; they may refer to the same combined PEM file. Whitespace surrounding a path and NUL bytes are rejected; spaces inside valid paths are supported.

Empty TLS paths represent incomplete provisioning; they do not authorize plaintext serving. The future startup layer must require usable TLS before opening application listeners. It must also check file contents, existing path aliases/symlinks, mount identities, write permissions and capacity. Configuration validation performs no filesystem operations or network/DNS activity and cannot establish these runtime conditions.

The tests cover defaults, process and injected environment parsing, empty values, full overrides, numeric port bounds, IPv4/IPv6 syntax, malformed addresses, storage path boundaries and overlaps, and paired TLS paths including combined PEM files. This is an initial slice of the server configuration work in implementation chunk 04, not completion of that chunk.
