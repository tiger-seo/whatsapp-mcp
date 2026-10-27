# Quickstart: validating feature 002

How to show that the storage works. Nothing here touches a real WhatsApp account or the network: every device and
message is synthetic. Expected behaviour is defined in [spec.md](spec.md); the interfaces are in
[contracts/](contracts/).

## Prerequisites

- Go 1.27.3 toolchain (from `go.mod`), `CGO_ENABLED=0`.
- Git Bash on Windows, or any POSIX shell.
- For the cross-identity checks only: a second local OS account (see step 6).

## 1. Build and unit/integration tests

```sh
CGO_ENABLED=0 go build ./...
CGO_ENABLED=0 go vet ./...
CGO_ENABLED=0 go test ./internal/store/... ./internal/private/... ./cmd/whatsapp-mcp/...; echo "exit=$?"
```

Expected: exit 0. Every test uses `t.TempDir()`. The suites cover:

- migrations: fresh, incremental, failing and too-new, for both schemas;
- preflight byte-for-byte refusals (SC-006);
- the name and last-activity rules in 20 random orders (SC-003);
- re-store and media keep;
- every query in FR-021–FR-034, including Unicode and literal search;
- the LID merge;
- validation fuzz seeds and every approved limit at its boundary and beyond (SC-009);
- admission, busy and cancellation;
- disk-low through an injected free-space function;
- secret-free logs at every level (SC-011).

## 2. Fuzz the validators (short local run)

```sh
go test ./internal/store -run '^$' -fuzz FuzzValidateMessage -fuzztime 60s
go test ./internal/store -run '^$' -fuzz FuzzNormalizePhone -fuzztime 30s
```

Expected: no failures. Any crasher is added to `testdata/fuzz` and fixed.

## 3. First start creates one file

```sh
D="$TEMP/wa-002"; rm -rf "$D"
go run ./cmd/whatsapp-mcp stdio --data-dir "$D" </dev/null; echo "exit=$?"
ls -la "$D"
```

Expected:

- `stdio` starts, sees stdin close, and exits 0;
- the log shows `store.preflight fresh=true`, `store.upgrade schema=device from=0 to=16`,
  `store.migrate schema=messages ... to=1` and `store.ready`, all with UTC times;
- `$D` holds `whatsapp.db` and the lock file, and possibly `-wal`/`-shm`, which SQLite removes on a clean close;
- on Unix every file is `-rw-------`;
- on Windows, `icacls "$D\whatsapp.db"` lists only this user, SYSTEM and Administrators.

Run it again: `fresh=false`, no `store.upgrade`/`store.migrate` lines, exit 0.

## 4. Refusals leave files untouched

```sh
go run ./internal/store/testdata/cmd/bumpversion "$D/whatsapp.db" messages 99   # test helper
sha256sum "$D"/whatsapp.db > "$TEMP/before.txt"
go run ./cmd/whatsapp-mcp stdio --data-dir "$D" </dev/null; echo "exit=$?"
sha256sum -c "$TEMP/before.txt"; ls "$D"
```

Expected:

- exit 1;
- stderr names the message schema and both versions;
- the checksum matches and no `-wal`/`-shm` appeared.

Repeat with `device 99` and with a file of random bytes; the outcome is the same, with the matching message. The
automated version of this step (SC-006) also covers a WAL holding committed data.

## 5. Performance benchmark (SC-004, not a CI gate)

```sh
go test ./internal/store -run '^$' -bench BenchmarkQueries -benchtime 20x -count 3 | tee "$TEMP/bench.txt"
```

The benchmark seeds 100,000 messages across 1,000 chats once, then runs every query in FR-021–FR-033. Expected:
every `ns/op` is below 200 ms. Record the output, the date and the machine (CPU, RAM, disk, OS) in
`specs/002-sqlite-storage/verification.md`.

## 6. Cross-identity access (SC-008, manual with recorded evidence)

Unix:

1. As this user, run step 3.
2. As a second user, run `cat "$D/whatsapp.db"` and `ls "$D"`.
3. Expected: permission denied for both.

Windows:

1. Run step 3.
2. Run `runas /user:<other> "cmd /c type %TEMP%\wa-002\whatsapp.db"`.
3. Expected: access denied.

Record commands, dates and results in `verification.md`. A platform that was not tested is marked "not verified",
never "passed".

## 7. Backup and restore rehearsal (SC-010)

Follow `OPERATIONS.md` "Storage: backup and restore" with the synthetic data from step 3, plus messages written
by the test helper `go run ./internal/store/testdata/cmd/seed`:

1. Stop the process.
2. Copy the file set to a private backup directory.
3. Restore it into a new private directory.
4. Run the documented offline checks.

Expected: `integrity_check` returns `ok`, `foreign_key_check` returns no rows, both versions match, and the seeded
counts match. Neither copy is ever connected to WhatsApp.

## 8. Lint and CI parity

```sh
gofmt -l . ; go vet ./... ; govulncheck ./...
git ls-files --eol | grep 'w/crlf' || echo "LF ok"
```

Expected:

- no files listed by `gofmt`;
- no reachable vulnerabilities, or exceptions recorded in `verification.md`;
- "LF ok".
