# Quickstart: validating the binary skeleton

These scenarios prove that feature 001 works end to end with the real binary. Expected messages and exit codes
are defined in [contracts/cli.md](contracts/cli.md) and [contracts/configuration.md](contracts/configuration.md).
None of this touches WhatsApp.

## Prerequisites

- Go as pinned in `go.mod` (1.27.0). No C toolchain is needed.
- Git Bash on Windows, or any POSIX shell on Linux or macOS. The commands use `$TEMP` and `/dev/null`.
- On this Windows machine, point the Go caches at D: first (C: is nearly full):
  `export GOMODCACHE=D:/tmp/gocache/mod GOCACHE=D:/tmp/gocache/build GOTMPDIR=D:/tmp`.

Use a scratch data directory, so that the real per-user directory is not touched except in scenario 3:

```bash
export CGO_ENABLED=0
go build -o "$TEMP/wamcp/whatsapp-mcp" ./cmd/whatsapp-mcp
W="$TEMP/wamcp/whatsapp-mcp"
D="$(cd "$TEMP" && pwd -W 2>/dev/null || pwd)/wamcp/data"   # absolute, native form
```

## 1. Automated checks (what CI runs)

```bash
gofmt -l .            # expect: no output
go vet ./...          # expect: exit 0
go test ./...         # expect: ok for cmd/whatsapp-mcp, internal/config, internal/lock, internal/buildinfo
for t in windows/amd64 linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do
  GOOS=${t%/*} GOARCH=${t#*/} go build -o /dev/null ./cmd/whatsapp-mcp > "$TEMP/build.log" 2>&1
  echo "$t: $?"                                     # expect: 0 for all five
done
```

Read each exit code directly, not through a pipe.

## 2. `version` and help (US1)

| Command | Expect |
| --- | --- |
| `"$W" version; echo $?` | 5 lines (`whatsapp-mcp dev`, `commit:`, `built:`, `go:`, `target:`), exit 0 |
| `WHATSAPP_MCP_LOG_LEVEL=verbose WHATSAPP_MCP_DATA_DIR=relative "$W" version; echo $?` | same output, exit 0 |
| `"$W" --help; echo $?` | five commands listed on stdout, exit 0 |
| `"$W"; echo $?` / `"$W" frobnicate; echo $?` | usage on stderr, exit 2 |
| `"$W" stdio --help` | `--data-dir` and `--log-level`, each with its environment variable and default |

## 3. Configuration layers and errors (US2, US3)

```bash
mkdir -p "$D" && printf 'log_level = "warn"\n' > "$D/config.toml"
WHATSAPP_MCP_LOG_LEVEL=info "$W" status --data-dir "$D" --log-level debug 2>&1 | grep 'setting=log_level'
#   expect: value=debug source=flag
WHATSAPP_MCP_LOG_LEVEL=info "$W" status --data-dir "$D" 2>&1 | grep -c 'level=DEBUG'
#   expect: 0 (level is info, from the environment)
```

The other precedence cases are covered by `go test ./internal/config`. Spot-check the error paths:

| Setup | Command | Expect |
| --- | --- | --- |
| `WHATSAPP_MCP_LOG_LEVEL=verbose` | `"$W" status --data-dir "$D"` | exit 2; message names `log_level`, `WHATSAPP_MCP_LOG_LEVEL`, `"verbose"` and the accepted values |
| `config.toml`: `log_level = "info` | `"$W" status --data-dir "$D"` | exit 2; path, `line 1, column …` |
| `config.toml`: `loglevel = "info"` on line 2 | same | exit 2; `line 2: unknown key "loglevel"` |
| `config.toml`: `data_dir = "/x"` | same | exit 2; says to use `--data-dir` or `WHATSAPP_MCP_DATA_DIR` |
| `config.toml`: `log_level = 5` | same | exit 2; `log_level must be a string` |
| `LOG_LEVEL=debug`, valid file removed | same | level stays `info`, no warning |
| `WHATSAPP_MCP_LOGLEVEL=debug` | same | `WARN … unknown environment variable name=WHATSAPP_MCP_LOGLEVEL`, startup continues |
| — | `"$W" status --data-dir data` | exit 2; not an absolute path |
| `touch "$TEMP/wamcp/afile"` | `"$W" status --data-dir "$D/../afile"` | exit 1; names the path, "not a directory" |
| — | `"$W" status --data-dir "$D/new/nested"` | the directory is created (mode `drwx------` on Unix) |

**Working directory independence** (SC-006, which touches the real per-user directory, only to create it):
run `"$W" status --log-level debug 2>&1 | grep setting=data_dir` from two different directories (for example
`cd /` and `cd "$TEMP"`). The `value=` is the same both times, and it is the per-OS default.

## 4. Single instance (US4)

Terminal 1: `"$W" serve --data-dir "$D"`. It logs `serve: HTTP server not implemented yet` and keeps running.

Terminal 2:

| Command | Expect |
| --- | --- |
| `"$W" stdio --data-dir "$D" </dev/null; echo $?` | `another whatsapp-mcp (PID <pid of terminal 1>) is using the data directory <D>`, exit 1, in under 1 s |
| `"$W" pair --data-dir "$D"; echo $?` | the same "already running" message, exit 1 |
| `"$W" status --data-dir "$D"; echo $?` | `status is not implemented yet`, exit 1. It is **not** blocked by the lock. |
| `"$W" stdio --data-dir "$D-b" </dev/null; echo $?` | another directory: starts, stops at stdin EOF, exit 0 |

Kill terminal 1 hard (`kill -9 <pid>`, or `taskkill //F //PID <pid>` on Windows). Then
`"$W" stdio --data-dir "$D" </dev/null; echo $?` starts at the first attempt, exit 0.

## 5. Shutdown and clean stdout (US5)

| Action | Expect |
| --- | --- |
| `"$W" serve --data-dir "$D"`, then Ctrl+C in that terminal (**on Windows too**: this is the manual check for real Ctrl+C) | `shutting down reason=interrupt`, `stopped`, exit 0; the lock is free at once (scenario 4, row 1, now starts) |
| `kill -TERM <pid>` (Unix) | as above with `reason=terminated` |
| Ctrl+C, and a second Ctrl+C while shutdown runs | `forced exit`, non-zero exit (hard to hit by hand in this feature because the shutdown is instant; covered by the process tests) |
| `"$W" stdio --data-dir "$D" --log-level debug </dev/null >"$TEMP/out.bin"; echo $?; wc -c <"$TEMP/out.bin"` | exit 0; **0 bytes** on stdout; log lines on the terminal (stderr) |
| Any log line | `time=… level=… msg=…` plus `key=value` attributes |

## 6. CI (US6)

1. Push the branch. In GitHub Actions, check that `check`, the three `test (…)` jobs and the five `build (…)`
   jobs are green.
2. On a scratch branch, commit a deliberately unformatted Go file and push it. `check` must fail and print the
   file name. Delete the scratch branch afterwards.

## Recording results

Record the commands, the date and the counts (tests passed, targets built, CI run URL) in
`specs/001-binary-skeleton/verification.md` when implementation finishes.
