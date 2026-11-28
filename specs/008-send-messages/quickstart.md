# Quickstart: Validating Feature 008

All commands run from the repository root with `CGO_ENABLED=0`. Record each result (command, date, counts, exit code read from the command itself) in `verification.md`.

## 1. Offline gates (no account)

```sh
gofmt -l .
go vet ./...
go test ./internal/media ./internal/mcp ./internal/wa ./internal/config ./cmd/whatsapp-mcp -count=1
go test ./... -count=1 -timeout 30m
for t in windows/amd64 linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do GOOS=${t%/*} GOARCH=${t#*/} go build -o /dev/null ./cmd/whatsapp-mcp; done
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
```

Expected, per [tools.md](contracts/tools.md) and [send-pipeline.md](contracts/send-pipeline.md):

- Every tool, argument and error code has a case through the in-memory MCP transport with the fake send port. The fake records zero uploads and zero sends for every refusal before step 8.
- Ogg analysis matches the reference files to within 1 s. The fuzz target runs for its CI budget without a crash.
- Path abuse cases are refused on the platform that runs them (Windows: junctions, alternate data streams, UNC; Unix: symlinks, FIFOs, devices; all platforms: hard links, traversal, prefix look-alikes, relative paths).
- Conversion with a stand-in converter covers the stall, oversized output, cancellation and shutdown cases. No process and no working directory remain after 5 s.
- On the simulator, sends succeed, and a stop during the lookup, the upload and the wait for the acknowledgement passes the quiet-stop check.

## 2. Converter checks (real ffmpeg, no account)

With ffmpeg installed, run the converter integration test (guarded by an environment variable defined in the tasks). It converts every allowed input kind, refuses a playlist or concat file disguised as `.mp3`, and checks the output's duration. Record the ffmpeg version and the platform.

## 3. Real-account acceptance (each step only with the owner's permission)

Follow the project's real-account procedure (AGENTS.md): a new empty data directory under `D:\tmp`, the binary built from a named commit, the owner scans the QR code, and cleanup is unlinking on the phone and then deleting the directory.

1. `pair`, then start `stdio` with:
   - `send_allowed_recipients` set to the one chat the owner names;
   - `send_allowed_dirs` at its default, with test files placed in `<data dir>/outbox`.
2. From an MCP client, with separate permission for each send:
   - `send_message` with a short text;
   - `send_file` with a `.jpg` and a caption;
   - `send_file` with a `.pdf`;
   - `send_file` with an `.ogg`;
   - `send_audio_message` with an `.mp3` (needs ffmpeg).
3. On the phone, check each message's type, the caption, the document title, and that both voice notes play with a duration within 1 s of the source.
4. Check with `list_messages` that the five sends are stored with `is_from_me`.
5. Clean up as the procedure says. Record redacted results; any step not run is "not verified".
