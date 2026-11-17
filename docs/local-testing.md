# Local end-to-end testing

How to run a ServerSend server and the desktop app together on your own machine and try an upload. This exercises the [vertical slice](vertical-slice.md); read that page for what is and is not implemented yet.

## Prerequisites

Go (the version in `go.work`), Node.js with npm, and the native desktop build packages described in [desktop development](desktop-development.md). An authenticator app on your phone (any TOTP app) for the sign-in step.

## Quick start

Use two terminals, both at the repository root.

| Terminal | Command | What it does |
| --- | --- | --- |
| 1 | `make local-test-server-setup` | Walks through setup, creates the first administrator, then runs the server until you press Ctrl+C |
| 2 | `make local-test-desktop` | Builds and opens the desktop app |

### Terminal 1: the server

`make local-test-server-setup` asks three questions the first time. Press Enter to accept the value in brackets.

1. **Address clients will use.** Keep `127.0.0.1` to test on this computer only. To connect from another device, enter this computer's LAN or Tailscale IPv4 address; the script lists the addresses it finds.
2. **Port.** Default `58443`.
3. **Administrator username.** Default `admin`.

It then prints a **temporary password** (valid for one hour; copy it now, it is not shown again) and starts the server. Leave this terminal open. Note the log line:

```
Certificate fingerprint for desktop approval: 0ff6 5f99 3c3a …
```

### Terminal 2: the desktop app

`make local-test-desktop` installs frontend dependencies on first use, builds the app, prints the server address to enter, and opens the window.

### In the browser first

Open `https://SERVER_IP:58443/admin/`, sign in with the bootstrap administrator, replace its password, enroll an authenticator and save recovery codes. Finish sign-in with a fresh code, then create a user account. Use that user’s temporary password in the desktop. Administrator accounts can no longer transfer files.

### In the app

1. **Connect.** Enter the address the script printed, for example `https://127.0.0.1:58443`.
2. **Approve the server.** Check that the fingerprint in the app matches the one in terminal 1, then continue. If they differ, do not continue.
3. **Sign in** with the new user’s username and temporary password.
4. **Choose your password** (at least 12 characters).
5. **Set up two-factor sign-in.** Scan the QR code with your authenticator app and enter the six-digit code.
6. **Save the recovery codes.** Each works once in place of an authenticator code.
7. **Enter your code.** The code from step 5 is already used: wait for the authenticator to show the next one (up to 30 seconds), or enter one of the recovery codes.
8. **Create folder**, open the destination, then **Upload files…** or **Upload folder…**. Watch throughput in the Transfers chart; **Download…** saves a file back to a location you choose.

To confirm a round trip, compare a downloaded file with its original:

```sh
sha256sum original-file downloaded-file
```

## Running it again

| Goal | Command |
| --- | --- |
| Restart the server with the same accounts and files | `make local-test-server-setup` (it reuses the saved address and skips administrator creation) |
| Reopen the app | `make local-test-desktop`, then connect and sign in with your own password and a current authenticator code |
| Start over from nothing | `make local-test-reset` (asks you to type `delete`; removes accounts, files, certificates and keys), then run setup again and remove the old entry from your authenticator app |
| Change the address or port | Delete `settings.env` in the test directory, then run setup again; the app will ask you to approve the new certificate |

The app does not stay signed in between launches yet, so every launch repeats Connect, Approve, Sign in and Enter your code.

## Where things are

Everything for the test server lives in `~/serversend-demo` (set `SERVER_SEND_LOCAL_DIR` before both `make` commands to use another directory).

| Path | Contents |
| --- | --- |
| `config/serversend.sqlite` | Accounts, sessions and file metadata |
| `config/totp.key` | Key protecting authenticator secrets; without it enrolled accounts cannot sign in |
| `config/tls/` | The private certificate authority and server certificate |
| `data/<username>/` | Each user's files and folders, browsable (read-only for people) |
| `data/.serversend/` | Retained versions, unfinished uploads and in-progress changes |
| `settings.env` | The address and port you chose |
| `serversend` | The server binary built by the script |

## Troubleshooting

| Symptom | Cause and fix |
| --- | --- |
| "Credentials are invalid or expired" with the temporary password | It expired after one hour or was mistyped. Run `make local-test-server-setup` again and answer `y` to issue a new one. This works only until that administrator finishes two-factor setup. |
| A correct authenticator code is rejected right after setup | The setup code is spent. Wait for the next code or use a recovery code. |
| A correct code is rejected after several wrong ones | Each wrong code adds a growing delay (one second doubling up to five minutes). Wait, then try once with a fresh code. Also check that your phone's clock is accurate. |
| "Too many authentication attempts" | The server allows a burst of ten sign-in steps, refilling two per second. Wait a few seconds. |
| "could not reach a ServerSend server" | The server is not running, or the address or port differs from what setup printed. From another device, the server must have been set up with its LAN address, not `127.0.0.1`. |
| "The server's certificate does not match the one approved" | The server's certificate changed (renewal, new address, or reset). Go back to Connect and approve the new fingerprint after checking it against terminal 1. |
| Lost the authenticator and all recovery codes | There is no account recovery screen yet. Use `make local-test-reset` and set up again. |
| Port already in use | Another server is still running. Stop it with Ctrl+C in its terminal, or choose another port (see "Change the address or port"). |

## Automated checks

These need no manual steps and are what CI and `make check` rely on.

| Command | Covers |
| --- | --- |
| `make check` | Formatting, static checks, all Go and UI tests, production builds, binding comparison and the desktop smoke test |
| `cd apps/server && GOWORK=off go test -race ./...` | Server tests with the race detector, including the end-to-end test below |
| `cd apps/server && GOWORK=off go test -run EndToEnd ./internal/server/` | A real server over TLS driven by the real desktop client code: onboarding, uploads from empty to multi-chunk, byte-for-byte restore, account isolation, sign-out and revocation |
| `cd apps/desktop/frontend && npm test` | The desktop screen flow against mocked Go bindings |
| `make check-persistence` | Native SQLite builds and database tests |

The automated end-to-end test does not open the desktop window or native file dialogs; the manual walkthrough above is the only check of those.

For persistent Docker deployment and network measurements, see [Unraid testing](unraid-testing.md).

If Firefox reports `SSL_ERROR_NO_CYPHER_OVERLAP` with an older Docker deployment, see the [private HTTPS certificate migration](server-development.md#firefox-certificate-compatibility). Updating the image alone preserves the old TLS files, so affected installations need the documented one-time backup and certificate replacement.
