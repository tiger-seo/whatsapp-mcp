# Quickstart: Validating Message Ingest

How to show that feature 005 works. Every command uses `CGO_ENABLED=0` (except the race run), Git Bash on Windows,
and `TEMP`/`TMP` on `D:\tmp`. Behaviour is defined in [contracts/ingest.md](contracts/ingest.md); state and bounds are
in [data-model.md](data-model.md). Record each result in `verification.md` with the command, the date, the counts and
the exit code read directly (no pipe). No step uses a real account.

## 1. The dependency pin

```bash
go mod verify
grep -n 'whatsmeow' go.mod
```

Expected: `all modules verified`; `go.mod` replaces `go.mau.fi/whatsmeow` with
`github.com/tiger-seo/whatsmeow v0.0.0-20261113124132-bedcf0185245`. `TestQuietAllowListPin` compares against that
version.

## 2. Ingest on synthetic events

```bash
CGO_ENABLED=0 go test ./internal/wa -run 'TestIngest' -count=3 -timeout 20m
CGO_ENABLED=0 go test ./internal/store -run 'TestValidateMessage|TestMessageFieldBytes|TestPhoneForLID|TestMappedLIDsWithData|TestContactNames' -count=1
```

Expected, among others:

- **Live** (US1): text, each media type with caption and metadata, a duplicate, an unsupported type and an edit store
  what the data model says and acknowledge as invariant N1 says.
- **History** (US2): mixed order with live messages keeps the newest last activity; 600 messages become three
  batches of 250 (history flushes); a message without a time is rejected and its neighbours are stored; a history over 128 MiB, a full queue
  and a download error each report `history_incomplete` and keep the connection.
- **Identity** (US3): mapped and unmapped LIDs, a lookup that times out, a startup merge with a duplicate ID, a second
  startup that changes nothing, a mapping written during the run that is merged without a restart, and a mapping
  written inside an uncommitted transaction that is merged after it commits.
- **Names** (US4): a counted fake `GetGroupInfo` is called once for 100 messages in one group; a failure is not
  retried for 10 minutes; a full lookup set is counted; no name is ever downgraded.
- **Abuse**: oversized text, caption, name and media fields; malformed JIDs and timestamps; control characters in
  names; 10,000 distinct groups; a message whose text asks for a send. Nothing invalid is written, nothing is sent,
  and no bound in the data model is exceeded.

## 3. Failure and shutdown

```bash
CGO_ENABLED=0 go test ./internal/wa ./cmd/whatsapp-mcp -run 'TestIngest|TestSimulatorIngest|TestOwnerTerminal|TestQuietOrder|TestShellIngest' -count=3 -timeout 20m
```

Expected:

- A store error on a live message: no acknowledgement, `ingest_failed`, later messages dropped and counted, the
  command stops and exits 1 with the storage message, stdout empty in `stdio`, earlier messages intact.
- A failure in the middle of a history chunk: earlier batches stay, the report names the chunk, nothing claims
  completion.
- A full handler queue in the simulator: `receive_overload` logged with its reason, `Health().ReceiveOverload` set,
  no reconnect.
- A stop while the handler, the history worker, a group lookup and a merge are each in progress: the order test of
  feature 004 still passes, no message write follows the join, and dropped messages are reported, not hidden.

## 4. The full gates

The commands in `AGENTS.md` ("Commands"): gofmt, vet, `go mod verify`, `go test ./... -count=1 -timeout 30m`, the
five builds, govulncheck, and the session-check helper.

## 5. Measurements

Handler latency under history (research R3) and stops with every worker busy (R11):

```bash
WA_MEASURE=1 CGO_ENABLED=0 go test ./internal/wa -run 'TestIngestHandlerLatency|TestQuietStopWithIngestWorkers' -count=1 -v -timeout 60m
```

Expected: no `receive_overload` and the recorded lock-wait percentiles; the stop pass rate and `stop_bound_exceeded`
count with workers busy. The owner reviews both before the code review.

## 5a. Quiet-stop rate with synchronous acknowledgements

```bash
WA_MEASURE=1 CGO_ENABLED=0 go test ./internal/wa -run 'TestQuietStopPassRate' -count=1 -v -timeout 60m
```

Expected: at least feature 004's recorded pass rate for each kind of stop. A lower rate goes to the owner before any
user story starts (task T025).

## 6. Race detector (WSL2, cgo for this run only)

Needs `gcc` in the Ubuntu VM. Record as WSL2, not native Linux.

```bash
wsl -d Ubuntu --cd /mnt/d/projects-mou/whatsmeow-receive-bounds --exec bash -lc 'export PATH=$HOME/sdk/go1.27.3/bin:$PATH TMPDIR=/var/tmp CGO_ENABLED=1; go test -race ./... -count=1 -timeout 20m'
wsl -d Ubuntu --cd /mnt/d/projects-mou/whatsapp-mcp-next --exec bash -lc 'export PATH=$HOME/sdk/go1.27.3/bin:$PATH TMPDIR=/var/tmp CGO_ENABLED=1; go test -race -p 1 ./internal/wa ./cmd/... -count=1 -timeout 40m'
```

Expected: no race reported, exit 0.

## 7. Real account (only with permission for each step)

Not part of automated validation. With the owner's permission for each step, and after reviewing the roadmap's
shutdown follow-up: a new data directory under `D:\tmp`, the binary from a named commit, `pair` (record its `history_incomplete` counts by reason), then `serve` (record any history it receives); send
one text and one captioned image from another phone; stop; inspect the store; the owner unlinks the device and the
directory is deleted. Record redacted results; a step not run is "not verified".
