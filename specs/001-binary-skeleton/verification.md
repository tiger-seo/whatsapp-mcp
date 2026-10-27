# Verification: Binary skeleton

Run on 2026-10-27 on Windows 10 Pro 10.0.19045, Git Bash, Go 1.27.0 (`windows/amd64`), with
`CGO_ENABLED=0` and the Go caches on D: (`GOMODCACHE=D:/tmp/gocache/mod GOCACHE=D:/tmp/gocache/build
GOTMPDIR=D:/tmp`). Each exit code was read from the command itself, not through a pipe.

## Automated checks (quickstart §1)

| Command | Result |
| --- | --- |
| `gofmt -l .` | exit 0, no files listed |
| `go vet ./...` | exit 0 |
| `go test -count=1 ./...` | exit 0; 4 packages ok: `cmd/whatsapp-mcp`, `internal/buildinfo`, `internal/config`, `internal/lock` |
| `go test -count=1 -v ./...` | 60 top-level tests: 56 passed, 4 skipped, plus 32 subtests passed; 0 failed |
| `GOOS=<t> GOARCH=<a> go vet ./...` for the five targets | exit 0 for windows/amd64, linux/amd64, linux/arm64, darwin/amd64, darwin/arm64 |
| `GOOS=<t> GOARCH=<a> go build -o /dev/null ./cmd/whatsapp-mcp` for the five targets | exit 0 for all five |
| `go mod tidy`; `go.mod` | direct requirements `github.com/gofrs/flock v0.13.1`, `github.com/pelletier/go-toml/v2 v2.4.3`, `golang.org/x/sys v0.47.0`; nothing needs CGO |
| `git ls-files --eol` | no `w/crlf` entry |
| `grep -rnE '(FR\|SC)-[0-9]\|\bUS[0-9]\|\bD1[0-9]?\b\|\bR[0-9]+\b' --include=*.go .` | no matches |

Skipped on Windows, because they need Unix, and run in CI on Linux and macOS: `TestFileUnreadable` (chmod
000), `TestPrepareDataDirKeepsMode` (mode bits), `TestProcessTerminate` (SIGTERM). `TestHelperHolder` is
the helper process of the cross-process lock test and skips when run directly.

The Windows process test `TestProcessInterrupt` sent a real `CTRL_BREAK_EVENT` to a `serve` child in its own
process group. The child logged `shutting down reason=interrupt` and `stopped`, exited 0, and the lock was
free at once.

## Quickstart scenarios with the real binary

Binary built with `go build -o "$TEMP/wamcp/whatsapp-mcp.exe" ./cmd/whatsapp-mcp`; scratch data directory
`C:/Users/Tiger/AppData/Local/Temp/wamcp/data`. Removed afterwards.

| § | Scenario | Result |
| --- | --- | --- |
| 2 | `version` | 5 lines (`whatsapp-mcp dev`, `commit:  a0644b8…-dirty`, `built:`, `go:      go1.27.0`, `target:  windows/amd64`), exit 0 |
| 2 | `version` with `WHATSAPP_MCP_LOG_LEVEL=verbose WHATSAPP_MCP_DATA_DIR=relative` | same output, exit 0 |
| 2 | `--help` | five commands on stdout, exit 0 |
| 2 | no command / `frobnicate` | `missing command` / `unknown command "frobnicate"` + usage on stderr, exit 2 |
| 2 | `stdio --help` | both flags with env variable and default (`C:\Users\Tiger\AppData\Roaming\whatsapp-mcp`, `info`) |
| 3 | file `warn`, env `info`, flag `debug` | `setting=log_level value=debug source=flag origin=--log-level` |
| 3 | file `warn`, env `info` | 0 `level=DEBUG` lines |
| 3 | `WHATSAPP_MCP_LOG_LEVEL=verbose` | exit 2, names `log_level`, `"verbose"`, `WHATSAPP_MCP_LOG_LEVEL`, accepted values |
| 3 | `log_level = "info` | exit 2, `…config.toml line 1, column 18: basic strings cannot have new lines` |
| 3 | `loglevel = "info"` on line 2 | exit 2, `line 2: unknown key "loglevel"` |
| 3 | `data_dir = "/x"` | exit 2, says to use `--data-dir` or `WHATSAPP_MCP_DATA_DIR` |
| 3 | `log_level = 5` | exit 2, `log_level must be a string, not an integer` |
| 3 | `LOG_LEVEL=debug`, no file | level `info`, no warning, exit 1 (`status is not implemented yet`) |
| 3 | `WHATSAPP_MCP_LOGLEVEL=debug` | `level=WARN msg="unknown environment variable" name=WHATSAPP_MCP_LOGLEVEL`, startup continues |
| 3 | `--data-dir data` | exit 2, `is not an absolute path` |
| 3 | `--data-dir "$D/../afile"` (a file) | exit 1, `data directory …\afile: exists and is not a directory` |
| 3 | `--data-dir "$D/new/nested"` | created, exit 1 (`status is not implemented yet`) |
| 3 | working directory: `status --log-level debug` from `/` and from `$TEMP` | both `value=C:\Users\Tiger\AppData\Roaming\whatsapp-mcp source=default`. `%AppData%\whatsapp-mcp` did not exist before; the empty directory created here was removed afterwards |
| 4 | `serve` holding `$D`; `stdio` on `$D` | `another whatsapp-mcp (PID 17320) is using the data directory …\wamcp\data`, exit 1 in 54 ms |
| 4 | `pair` on `$D` | same message, exit 1 |
| 4 | `status` on `$D` | not blocked: `status is not implemented yet`, exit 1 |
| 4 | `stdio` on `$D-b` | starts, `shutting down reason="stdin closed"`, `stopped`, exit 0 |
| 4 | `taskkill //F` the holder, then `stdio` on `$D` | starts at the first attempt, exit 0 |
| 5 | `stdio --log-level debug </dev/null >out.bin` | exit 0, **0 bytes** on stdout; config, `lock acquired`, `shutting down`, `stopped` on stderr |
| 5 | log line format | every line `time=… level=… msg=…` plus attributes |

**Manual Ctrl+C (quickstart §5, row 1),** run by the user on 2026-10-27 in a Windows `cmd.exe` console:

| Command | Result |
| --- | --- |
| `whatsapp-mcp.exe serve`, then Ctrl+C | `serve: HTTP server not implemented yet`, then `shutting down reason=interrupt` and `stopped` 1 ms later |
| `whatsapp-mcp.exe serve --data-dir d:\tmp\somedata`, then Ctrl+C | same three lines |
| `whatsapp-mcp.exe serve --data-dir ./somedata` and `--data-dir somedata` | `data_dir: "…" from --data-dir is not an absolute path` |

`cmd.exe` does not print the exit code; exit 0 on this path is shown by the automated Ctrl+Break test above.
After the runs, each data directory held only an empty `whatsapp-mcp.lock`: the PID is cleared on a clean
exit.

SIGTERM (§5, row 2) runs only on Unix, and is covered by `TestProcessTerminate` in CI on Linux and macOS.

## CI (quickstart §6)

Run on 2026-10-27 on GitHub Actions in the public repository `tiger-seo/whatsapp-mcp`.

| Run | Commit | Result |
| --- | --- | --- |
| [40546442970](https://github.com/tiger-seo/whatsapp-mcp/actions/runs/40546442970), push to `001-binary-skeleton` | `2f7aad4` | success: `check`, `test (ubuntu-latest)`, `test (windows-latest)`, `test (macos-latest)`, and `build (…)` for windows/amd64, linux/amd64, linux/arm64, darwin/amd64 and darwin/arm64, 9 of 9 jobs |
| [40546723853](https://github.com/tiger-seo/whatsapp-mcp/actions/runs/40546723853), push to the scratch branch `scratch/ci-gofmt-check` | `2f3c104` (adds `cmd/whatsapp-mcp/zz_unformatted.go`, compiling but not formatted) | failure in `check` only, at the `gofmt` step: `These files are not gofmt-formatted: cmd/whatsapp-mcp/zz_unformatted.go`; the other 8 jobs passed |

The scratch branch was deleted locally and on GitHub afterwards.
