# Send Feasibility Gate

**Status**: Verified

The four items from the [plan's feasibility gate](plan.md#constitution-check) ran as scratch work outside `/speckit-implement`, on 2026-11-27, with synthetic data and no real account. No production code changed.

Scratch code, all removed afterwards:

- two standalone probe programs, kept outside the module;
- one untracked simulator test in `internal/wa`;
- a temporary branch `feasibility/008-scratch` with a probe-only workflow. It was based on `74bf9d7`, with the normal CI removed and probe commits `2ec1947` and `bdfa40d`. The branch is deleted, and the run logs stay at the run links below.

This record is neither product implementation nor owner approval. Implementation still needs a fresh owner design `PASS` that covers this file.

**Toolchain**: Go 1.27.4 (WSL2 1.27.3), `CGO_ENABLED=0`, the pinned whatsmeow fork `bedcf01`, `golang.org/x/sys` v0.50.0 (already a module dependency).

**Platforms**:

- local Windows 10 (amd64), and WSL2 Ubuntu (amd64), recorded as WSL2, not native Linux;
- GitHub-hosted `ubuntu-latest` (linux/amd64), `windows-latest` (windows/amd64), `macos-latest` (darwin/arm64) and `macos-15-intel` (darwin/amd64).

**CI runs**:

- [45997516073](https://github.com/tiger-seo/whatsapp-mcp/actions/runs/45997516073) at `2ec1947`: the first run. The converter failed on Windows and both macOS runners; the findings are below.
- [45998881113](https://github.com/tiger-seo/whatsapp-mcp/actions/runs/45998881113) at `bdfa40d`: the final run. All four jobs succeeded.

| Item | Result |
| --- | --- |
| 1. Simulator send and stop | Verified on Windows, WSL2 and all four runners |
| 2. Upload deadline and memory | Verified on Windows, WSL2 and all four runners |
| 3. File containment | Verified on Windows, WSL2 and all four runners (user and root) |
| 4. Converter | Verified on Windows and all four runners after the process-tree change; some inputs only where the build could encode them |

## Item 1: simulator send

**Scratch test**: `internal/wa/feas008_scratch_test.go`, which uses the existing owner rig (`newRecordedRig`, `pairAndConnect`). A test IQ handler answered:

- the device list,
- the prekey fetch,
- the registration lookup (`jid` = LID, `pn_jid` = phone for a registered number; `type="out"` otherwise),
- the user-info lookup with `<lid val=…>`.

A routed transport served uploads, and the test acknowledged every outgoing message node. `internal/wasim` was not changed.

**Command:** `go test ./internal/wa -run TestFeas008 -count=1 -v`, then `-run TestFeas008StopDuringEachStep -count=20`. Exit 0 locally, in WSL2 and on all four runners in run 45998881113.

| Check | Result |
| --- | --- |
| Lookup | `IsOnWhatsApp` returned registered with the LID for the peer, and unregistered for an unknown number. |
| Text send | Succeeded with a message ID and a server timestamp. |
| Image send | `UploadReader` of 1 MiB, then `SendMessage` with caption: succeeded; 1,048,602 encrypted bytes uploaded. |
| Quiet stop after the sends | Passed: 0 library goroutines, 0 refused network, storage or calls. |
| Stop during each step | Send held at the lookup, device list, prekeys, upload or acknowledgement wait, under the generation's work context. Every case passed the quiet-stop check (0 library goroutines, 0 refusals). In the running generation's retirement the send returned right after `work_cancelled`, before `disconnect`, `fence_closed` and `barrier_retired` (asserted in the final run). |
| Repetition | 20 × 5 stop cases per platform, exit 0 on all four runners; 3 more locally after the assertion fix, exit 0. |

**Finding (addressing)**: For a phone recipient without a stored LID, the pinned library looks the user up itself inside `SendMessage`, and refuses an unregistered number. That lookup runs after any upload. The early registration lookup stores the LID mapping, so the send does not repeat it. Recorded in [research R9](research.md#r9-outcome-and-local-record).

## Item 2: upload deadline and memory

**Same scratch test** (`TestFeas008UploadDeadlineSplit`, `TestFeas008UploadMemory`). The candidate split transport wraps the production `boundedTransport` and fence. Uploads are bounded by the caller's deadline; other requests by a 10-second context, with the response limit unchanged.

| Check | Windows local | All four runners |
| --- | --- | --- |
| Existing 10-second client, 12 MiB upload throttled to about 1 MiB/s | Failed at 10.11 s (`context deadline exceeded`) | Failed at the 10-second bound |
| Split client, 2-minute deadline | Completed in 12.21 s, 12,582,938 bytes received | Completed in 12.07–12.45 s |
| Split client, 3-second deadline | Stopped at 3.0 s | Stopped at about 3 s |
| Stalled download through the split client | Stopped at 10.0 s | Stopped at 10.0 s |
| 100 MiB `UploadReader` (file → private temp file) | +0 MiB heap in use | +0 MiB |
| 100 MiB `Upload` (byte slice) | +200 MiB | +199–200 MiB |

Download bounds are unchanged: the production client was not modified, and the split keeps the 10-second total for non-upload requests. Implementation must keep the existing HTTP tests green.

## Item 3: containment

**Probe**: a standalone program (standard library only) implementing [research R4](research.md#r4-path-containment-feasibility-on-each-platform). It resolves with `EvalSymlinks`, picks the root, does a confined `os.Root` open, then checks the handle for a regular file, one link and 1 byte to 100 MiB. Command: `go run .` (exit 0 everywhere); on the Unix runners also `sudo ./probe`.

| Platform | Passed | Skipped | Failures |
| --- | --- | --- | --- |
| Windows local | 22 | 8 (symlink creation needs a privilege; 8.3 names off) | 0 |
| WSL2 | 21 | 1 (device node needs root) | 0 |
| `windows-latest` | all, including symlinks and the 8.3 short name | 0 | 0 |
| `ubuntu-latest`, `macos-latest`, `macos-15-intel` | all (user) and all including the device node (root) | 1 as user (device node) | 0 |

**Refused cases:**

- relative path, traversal, prefix look-alike, path outside the allowed directories;
- directory, empty file, file over 100 MiB, missing file;
- hard link to an outside file;
- symlink to an outside file, symlinked directory escaping the outbox;
- a directory swapped for a symlink (Unix) or a junction (Windows) between resolution and open, refused by `os.Root` ("path escapes from parent");
- Windows: junction to outside, alternate data stream, UNC admin share (`\\localhost\d$`), `\\?\`, `\\.\` and forward-slash UNC, reserved names `CON` and `NUL.txt`;
- Unix: FIFO, and a regular file swapped for a FIFO after resolution. Both refused without blocking, thanks to `O_NONBLOCK`. A symlink to `/dev/null`, and a character device (root runs).

**Accepted cases:**

- regular and nested files;
- symlinks to files inside the outbox, relative and absolute;
- a symlink into the other allowed directory (now accepted, as FR-006 says; see R4);
- a path through a symlinked prefix of the outbox;
- a case alias of the root and forward slashes (Windows).

**Informational (Windows):** A trailing dot or space, and the 8.3 short name, open the same file inside the outbox.

**Finding**: On Windows a path through any junction does not resolve, so it fails closed, even when the junction points inside the outbox. This is recorded in R4 and the tool contract.

## Item 4: converter

**Probe**: a standalone program implementing [research R6](research.md#r6-conversion-with-ffmpeg-feasibility):

- a private working directory, the input copied as `input`;
- an explicit demuxer, `-protocol_whitelist file,pipe` and the R6 encoder flags;
- the output read from stdout through a counting writer;
- an empty environment;
- a process tree stopped as a whole.

It also contains a prototype of the R5 Ogg/Opus duration parser. Each limit case runs twice: directly, and through a launcher that starts the real ffmpeg as a child and does not pass a kill on (a Go program on Windows, a shell script on Unix).

**ffmpeg versions**:

- local: 9.1 full build (gyan.dev, via scoop);
- `windows-latest`: 9.1 essentials (gyan.dev, via Chocolatey);
- `ubuntu-latest`: 8.0.1-3ubuntu2;
- `macos-latest`: 9.0.2;
- `macos-15-intel`: 9.0.1.

| Check | Result |
| --- | --- |
| Conversion of each 12-second input to Ogg Opus, duration by the R5 parser | 12 s for every input the platform's build could create. mp3, wav, m4a (index after the media data), aac (ADTS), flac and opus everywhere. Ogg Vorbis on Windows and Ubuntu (the macOS builds have no Vorbis encoder to create the input). amr on Windows only (no AMR encoder elsewhere). Decoding those two on the other platforms is not verified. |
| Direct Ogg Opus parse | 12 s. Ogg Vorbis refused as a voice note ("not Ogg Opus"). |
| Disguised inputs: an HLS playlist and an `ffconcat` file named `.mp3`, an HLS playlist named `.m4a`, a playlist with a network URL named `.wav` | All refused by the explicit demuxer, with no output, on every platform. |
| Time limit (2 s, on a 10-minute input read at native rate) | Stopped at 2.00 s and returned by 2.02 s, directly and through the launcher, with nothing left (working directory removed, process tree gone). |
| Output limit (16 KiB) | Stopped at 5.6–5.8 s, nothing left, both ways. |
| Cancellation at 1 s | Returned at 1.00–1.01 s, nothing left, both ways. |
| Negative control (process-tree handling off, `FEAS_NO_TREE=1`) | Windows local: the launcher cases failed. The real ffmpeg outlived the limits and kept `input` open, so the directory could not be removed. On `windows-latest` even the direct cases failed, because Chocolatey's `ffmpeg.exe` is itself such a launcher. On the Unix runners the control passed, which proves nothing: without a process group the probe's group check is trivially true, and the child probably dies when its output pipe closes. Unix process-group handling is therefore evidenced by the positive cases only. |

**Findings, folded into [research R6](research.md#r6-conversion-with-ffmpeg-feasibility) and the [pipeline contract](contracts/send-pipeline.md):**

- **Output-size polling:** Polling an output file's size does not enforce the limit on Windows; the size lags while ffmpeg writes. The first run's output-limit case ran to the full 60 s. The output now goes through a counted stdout pipe.
- **Launchers:** A package-manager launcher does not pass a kill on. In the first CI run on `windows-latest` the real converter outlived every limit. The converter now runs as a process tree that is stopped as a whole: on Windows, started suspended in a kill-on-close job object, then resumed; on Unix, in its own process group.
- **Local cleanup:** The local negative-control run left two scratch working directories. They were removed by name; both orphaned processes had already exited.

## Limitations

- **Simulator, not the real service:** Items 1 and 2 use the simulator and a local upload endpoint. The real server's replies, the media CDN and real upload speeds are verified only by the real-account acceptance in [quickstart.md](quickstart.md), which needs the owner's permission for each step.
- **Not exercised here:**
  - group sends (group info, sender keys), video and document sends, and a stop during a group send;
  - the end-to-end path through the MCP tools.

  The implementation tasks must cover these on the simulator. The quiet-stop allow-list needed no change for the paths exercised.
- **Converter inputs:** Vorbis and AMR decoding are verified only on the platforms listed above. OS-level sandboxing of ffmpeg remains a documented residual risk (spec Assumptions), not a control.
- **Hosted runners:** Intel macOS ran on the hosted `macos-15-intel` image only; no physical Mac was used.
