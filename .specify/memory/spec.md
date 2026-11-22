# Main Project Specification
> **Revision**: 2026-11-22 — Archived specs/001-binary-skeleton to preserve the baseline CLI, configuration, lifecycle and CI specification.
> **Revision**: 2026-11-22 — Archived specs/002-sqlite-storage to add the SQLite storage, query and migration contracts.
> **Revision**: 2026-11-22 — Archived specs/003-whatsapp-pairing-status to record the pairing, offline status, version selection, guarded device writes and safe recovery baseline.
> **Revision**: 2026-11-22 — Archived specs/004-shutdown-drain to define quiet-stop reuse, fresh-client pairing handover and their evidence; retired three confirmed entries from the blanket recovery baseline.
> **Revision**: 2026-11-22 — Reconciled the earlier marker and reconnect clauses with FR-110 through FR-113; retained the recovery and durability safeguards and the historical feature 003 artifacts.
> **Revision**: 2026-11-22 — Archived specs/005-message-ingest: live and history ingest, LID reconciliation, name resolution, and bounded failure handling.
> **Revision**: 2026-11-22 — Aligned the quiet-stop event count in FR-109 with FR-138: dropped ingest messages are reported separately and do not fail the check.
> **Revision**: 2026-11-22 — Archived specs/006-mcp-stdio-tools: five local read tools, bounded transport and store isolation, and protocol-only stdout replacing the skeleton's zero-byte criterion.
> **Revision**: 2026-11-22 — Clarified Story 5 and FR-095 so the earlier empty-stdout and no-tools baselines are not mistaken for current stdio requirements.
> **Revision**: 2026-11-22 — Aligned shutdown, offline status and shell acceptance with the current contracts; retained the archived baselines.

## Configuration Decisions

- **Log level variable.** M10 names the variable `LOG_LEVEL`, but C1 and D10 require every environment variable
  to carry the `WHATSAPP_MCP_` prefix, with no aliases. D10 wins: the variable is **`WHATSAPP_MCP_LOG_LEVEL`**.
  The flag is `--log-level` and the config file key is `log_level`. Plain `LOG_LEVEL` is not read. M10's intent
  (one logger whose level the operator sets) is unchanged; only the name follows D10. `docs/specification.md`
  M10 is updated to name `WHATSAPP_MCP_LOG_LEVEL`. [Source: .specify/archive/001-binary-skeleton/spec.md -> "Log level variable."]

- **Invalid log level.** The old bridge silently fell back to `info` for an unknown value (§3.1). C4 overrides
  that: an unknown value fails at startup. [Source: .specify/archive/001-binary-skeleton/spec.md -> "Invalid log level."]

## Storage Decisions

- **Two schema histories in one file.** whatsmeow keeps its own schema version for its device store tables. The
  message tables get their own, separate version history, so that a whatsmeow upgrade and one of our migrations
  do not share version records. Compatibility of both is checked before either is changed; supported device
  upgrades run before message migrations. The store is usable only after both finish. A committed earlier step
  remains committed if a later step fails; startup as a whole is not one transaction. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Two schema histories in one file."]

- **A database newer than the binary is refused.** If either schema is newer than the bundled implementation
  supports, opening fails during read-only preflight, naming the schema and both versions. The main file and
  existing sidecars are not changed, and no sidecars are created or removed. There is no downgrade path
  (Principle IX). Migration rollback instead preserves logical schema, data and version at that step's boundary;
  it does not promise byte-for-byte unchanged SQLite files. [Source: .specify/archive/002-sqlite-storage/spec.md -> "A database newer than the binary is refused."]

- **Chat names have a quality.** "Never gets worse" (S4) needs a rule. A name has one of three qualities, from
  highest to lowest:
  - a *saved name*: a contact's full or first name from the account's own address book;
  - a *real name* chosen by someone else: a group subject, a contact's push or business name, a history-sync
    display name;
  - a *fallback* derived from the JID: a phone number, `Group <id>`, the JID's user part.

  The account owner's own label therefore outranks the name a sender picked for themselves, so a sender cannot
  make a chat look like someone else's by choosing a misleading push name. Every offered name carries the time it
  was seen. A stored name is replaced only by a non-empty name of a higher quality, or of the same quality seen at
  the same time or later, so a renamed group or contact is picked up. At equal quality and time the byte-wise
  greater name wins, so the result never depends on the order of arrival. Empty never replaces anything. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Chat names have a quality."]

- **Re-storing a message replaces it.** Writing a message whose `(chat_jid, id)` already exists replaces its stored
  fields with the new ones (history sync and live delivery carry the same data, and a later copy may carry fresher
  media URLs). An empty media field in the new copy never overwrites a stored non-empty one, so an incomplete copy
  cannot make media undownloadable. The chat's `last_message_time` still only moves forward. This is the one case
  where the order of arrival matters: when two copies differ, the one written last is kept. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Re-storing a message replaces it."]

- **A contact is found under either identity.** Every query that takes a contact accepts a phone number, a phone
  JID or a LID (M6). The store resolves it through the device store's LID-to-phone map, and then matches messages
  and chats stored under the phone number, under the LID, or under both. A message stored under a LID before its
  phone number was known is therefore still found by the phone number once the map has it. [Source: .specify/archive/002-sqlite-storage/spec.md -> "A contact is found under either identity."]

- **The `@lid` merge drops duplicates instead of skipping the chat.** In the old bridge (§3.5) a chat was skipped
  when both chats held the same message ID. Here the phone-number chat's copy is kept, the LID chat's copy is
  dropped, and the merge completes. [Source: .specify/archive/002-sqlite-storage/spec.md -> "The `@lid` merge drops duplicates instead of skipping the chat."]

- **The merge rewrites the sender everywhere.** Merging a LID into its phone number also rewrites every message
  sent under that LID, in every chat (groups included), to the phone JID and number. It does this even when no
  `@lid` chat exists, so a person who only ever wrote in groups under a LID is merged too. [Source: .specify/archive/002-sqlite-storage/spec.md -> "The merge rewrites the sender everywhere."]

- **Context is chronological.** Messages before and after a message (for `get_message_context` and
  `include_context`) are both returned oldest first. The old bridge returned "before" newest first; the tools
  (006) decide how to present them. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Context is chronological."]

- **Last interaction** with a contact is the newest message that is either in the direct chat with that contact
  (sent by either side) or sent by that contact in any group, under either of the contact's identities. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Last interaction"]

- **Text search ignores case in every script**, not only ASCII, so Cyrillic, Greek and accented Latin match
  regardless of case. Both sides are normalised to one Unicode form (NFC) first, so a composed `é` matches `e` plus
  a combining accent. Accents still count: `ete` does not match `été`. Wildcard characters typed by the caller
  (`%`, `_`) are matched literally. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Text search ignores case in every script"]

- **The long-running commands open the store.** `stdio`, `serve` and `pair` open (and if needed create and migrate)
  the database after taking the lock and validating storage permissions and paths, so a broken, unsafe or too-new
  database is reported at startup before features 003–006 use it. `status` and `version` do not open it in this feature. [Source: .specify/archive/002-sqlite-storage/spec.md -> "The long-running commands open the store."]

- **Private storage is not encryption.** Directory access and sensitive SQLite artifacts are checked on every open,
  including Windows ACLs. The operator owns disk/backup encryption and protection against a compromised host or
  the same unrestricted OS identity. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Private storage is not encryption."]

- **Store limits are defensive, not tool defaults.** Direct store callers are subject to bounded inputs, batches,
  results, context expansion, concurrency and waits. Their values are the approved storage acceptance profile
  below, fixed in code; performance targets are not resource ceilings. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Store limits are defensive, not tool defaults."]

## User Scenarios & Testing

The users of the binary skeleton are the operator who installs and runs it and the developers who build on it. [Source: .specify/archive/001-binary-skeleton/spec.md -> "The "users" of this feature"]

**Scope:** Placeholder behavior, the absence of protocol traffic, and the associated 10-second shutdown cases below describe the binary skeleton before account, storage or transport work was added. [Source: .specify/archive/001-binary-skeleton/spec.md -> "Out of scope"]

### User Story 1 - Identify the installed binary (Priority: P1)

[Source: .specify/archive/001-binary-skeleton/spec.md -> User Story 1]

An operator downloads `whatsapp-mcp` and runs `whatsapp-mcp version` to confirm which build they have before
wiring it into Claude Desktop or a service manager.

**Why this priority**: `version` is the only command that does real work in this feature, and it proves the
binary builds and runs on every target platform.

**Independent Test**: Build the binary, run `whatsapp-mcp version`, and check the output and the exit code.

**Acceptance Scenarios**:

1. **Given** a release build, **When** the operator runs `whatsapp-mcp version`, **Then** stdout shows the
   version, the source commit, the build date, the toolchain version and the target OS/architecture, and the
   exit code is 0.
2. **Given** a local development build with no version information injected, **When** the operator runs
   `whatsapp-mcp version`, **Then** the version reads `dev` and the other fields show what is known or
   `unknown`.
3. **Given** a broken `config.toml` or an unusable data directory, **When** the operator runs
   `whatsapp-mcp version`, **Then** it still prints the build information and exits 0, because `version` reads no
   configuration.
4. **Given** any state, **When** the operator runs `whatsapp-mcp` with no command, with `--help`, or with an
   unknown command, **Then** the five commands are listed with their purpose; an unknown or missing command exits
   with the usage exit code.

---

### User Story 2 - Configure the binary in layers and get clear errors (Priority: P1)

[Source: .specify/archive/001-binary-skeleton/spec.md -> User Story 2]

An operator sets options in whichever place suits the deployment: a `config.toml` in the data directory for a
long-running daemon, `WHATSAPP_MCP_*` environment variables in a Claude Desktop config, or a flag for a one-off
run. When a value is wrong, the binary refuses to start and says which setting, which source and why.

**Why this priority**: every later feature adds settings to this mechanism. C4 requires that bad configuration
never surfaces later as a runtime failure.

**Independent Test**: Set the same setting in different sources and check which one wins; set invalid values in
each source and check the startup error. Automated tests cover both (Q3 "config precedence and validation").

**Acceptance Scenarios**:

1. **Given** `log_level = "warn"` in `config.toml`, `WHATSAPP_MCP_LOG_LEVEL=info` in the environment and
   `--log-level debug` on the command line, **When** a command starts, **Then** the effective level is `debug`.
2. **Given** the same file and environment but no flag, **When** a command starts, **Then** the level is `info`.
3. **Given** only the file setting, **When** a command starts, **Then** the level is `warn`.
4. **Given** no setting anywhere, **When** a command starts, **Then** the level is the default, `info`.
5. **Given** `WHATSAPP_MCP_LOG_LEVEL=verbose`, **When** a command other than `version` starts, **Then** it exits
   with the configuration exit code before doing anything else, and the message names the setting, the source
   (`WHATSAPP_MCP_LOG_LEVEL`), the rejected value and the accepted values.
6. **Given** a `config.toml` that is not valid TOML, has a key the binary does not know, or has a value of the
   wrong type, **When** a command starts, **Then** it fails with a message that names the file and the line or
   key.
7. **Given** the old variable `LOG_LEVEL=debug` and nothing else, **When** a command starts, **Then** the level
   stays `info`: old names are not read and produce no warning (D10).

---

### User Story 3 - Data lives in one predictable per-user directory (Priority: P1)

[Source: .specify/archive/001-binary-skeleton/spec.md -> User Story 3]

An operator runs the binary from Claude Desktop, a terminal or a service manager, each with a different working
directory, and every run uses the same data directory. They can point it elsewhere with `--data-dir` or
`WHATSAPP_MCP_DATA_DIR`.

**Why this priority**: the lock, the configuration file and, later, the database all live in the data directory.
C2 forbids any dependency on the working directory.

**Independent Test**: Run a command from two different working directories without overrides and check that both
use the same directory; run with an override and check that it is used and created when missing.

**Acceptance Scenarios**:

1. **Given** no override, **When** a command that needs the data directory starts, **Then** it uses
   `whatsapp-mcp` inside the operating system's per-user configuration directory (for example
   `%AppData%\whatsapp-mcp` on Windows, `~/Library/Application Support/whatsapp-mcp` on macOS and
   `$XDG_CONFIG_HOME/whatsapp-mcp` or `~/.config/whatsapp-mcp` on Linux), whatever the working directory is.
2. **Given** `--data-dir` pointing to a directory that does not exist, **When** the command starts, **Then** the
   directory is created, readable and writable only by the current user where the OS supports that.
3. **Given** a relative `--data-dir` or `WHATSAPP_MCP_DATA_DIR`, **When** the command starts, **Then** it fails
   with a configuration error asking for an absolute path.
4. **Given** a data directory path that is an existing file, or that cannot be created, **When** the command
   starts, **Then** it fails with a message naming the path and the reason.
5. **Given** an environment where the OS cannot report a per-user configuration directory and no override is
   set, **When** the command starts, **Then** it fails and the message suggests `--data-dir` or
   `WHATSAPP_MCP_DATA_DIR`.

---

### User Story 4 - Only one instance owns the data directory (Priority: P1)

[Source: .specify/archive/001-binary-skeleton/spec.md -> User Story 4]

An operator starts `whatsapp-mcp serve` as a daemon and later, by mistake, a second instance on the same data
directory (for example Claude Desktop launching `stdio`). The second instance refuses to start and tells the
operator which process holds the directory.

**Why this priority**: two processes sharing one WhatsApp session and one database corrupt both (A2, constitution
Principle III). Roadmap "done when": a second instance fails with a message that names the first instance's PID.

**Independent Test**: Start one instance that holds the lock, start a second on the same data directory, and check
the second one's error and exit code; kill the first one abruptly and check that a new instance then starts.

**Acceptance Scenarios**:

1. **Given** a running instance with PID 4242 holding the data directory, **When** a second `stdio`, `serve` or
   `pair` starts on the same data directory, **Then** it exits with the "already running" exit code and a message
   that says another `whatsapp-mcp` (PID 4242) is using the data directory, and names the directory.
2. **Given** the holder crashed or was killed without cleanup, **When** a new instance starts, **Then** it takes
   the lock without any manual step: there is no stale lock.
3. **Given** a running instance on data directory A, **When** another instance starts on data directory B,
   **Then** both run.
4. **Given** a running instance, **When** `whatsapp-mcp version` runs, **Then** it is not blocked by the lock.
   **When** `whatsapp-mcp status` targets the same locked directory, **Then** it exits 1 with the lock-holder
   diagnostic, without inspecting the database or claiming to report the owner's live state. The successful
   offline snapshot is covered separately in Story 16. [Source: .specify/archive/003-whatsapp-pairing-status/contracts/cli.md -> "Existing held lock, even before DB creation"]

---

### User Story 5 - Clean shutdown and clean output channels (Priority: P2)

[Source: .specify/archive/001-binary-skeleton/spec.md -> User Story 5]

An operator stops the daemon with Ctrl+C in a terminal (including on Windows), or a service manager or Claude
Desktop stops it with SIGTERM. The process stops in an orderly way and releases the data directory. In `stdio`
mode nothing but protocol traffic ever reaches stdout.

**Why this priority**: later features hang the WhatsApp client, the database and the HTTP server on this shutdown
path. A stray byte on stdout breaks the MCP client (Principle IV).

**Independent Test**: Start `serve`, send each stop signal, check the exit code and that the lock is free
afterwards. Run `stdio` with a local MCP client and debug logging; check that the client can initialize,
that stdout contains only MCP protocol traffic, and that logs go to stderr. Empty stdout was expected
for the feature 001 skeleton, which did not yet serve MCP.

**Acceptance Scenarios**:

1. **Given** a running `serve` or `stdio`, **When** it receives SIGINT or SIGTERM (or Ctrl+C in a Windows
   console), **Then** it logs that it is shutting down, drains before releasing the lock, and exits 0 only
   if orderly shutdown succeeds within the bound and no failure is latched. See FR-029.
2. **Given** a shutdown in progress, **When** a second stop signal arrives, **Then** the process exits at once
   with a non-zero code.
3. **Given** `stdio` started by an MCP client, **When** the client closes the process's stdin, **Then** the
   process follows the same orderly shutdown and exit rules. [Source: .specify/archive/006-mcp-stdio-tools/contracts/stdio.md -> "Normal EOF between frames, normal client disconnect or first stop signal"]
4. **Given** `stdio` at log level `debug`, **When** it starts, exchanges MCP messages and shuts down,
   **Then** stdout contains only MCP protocol traffic, and every log line is on stderr. See SC-046.
   [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> SC-007]
5. **Given** any command at any level, **When** it logs, **Then** each line carries a timestamp, a level and a
   message with structured key-value attributes, and lines below the configured level are not written.

---

### User Story 6 - Every change is checked on every target (Priority: P2)

[Source: .specify/archive/001-binary-skeleton/spec.md -> User Story 6]

A developer pushes a branch or opens a pull request, and CI reports whether the code is formatted, passes static
checks and tests, and builds for all five targets without a C toolchain.

**Why this priority**: the constitution requires every change to build for all five targets with
`CGO_ENABLED=0`, and `gofmt`, `go vet` and `go test ./...` to pass before a feature is done (Q2).

**Independent Test**: Push a commit and read the CI result; push one with an unformatted file and check that CI
fails on the format step.

**Acceptance Scenarios**:

1. **Given** a push to any branch or a pull request, **When** CI runs, **Then** it checks formatting, runs the
   static checks and the full test suite, and builds windows/amd64, linux/amd64, linux/arm64, darwin/amd64 and
   darwin/arm64 with CGO disabled.
2. **Given** a file that is not `gofmt`-formatted, **When** CI runs, **Then** it fails and names the file.
3. **Given** a change that breaks the build for one target only, **When** CI runs, **Then** it fails and names
   that target.

---

The "users" of this feature are the operator, who sees one database file in the data directory, and the developers
of features 003–008, who call the store operations. [Source: .specify/archive/002-sqlite-storage/spec.md -> "The users of this feature"]

### User Story 7 - One database file, created on first start (Priority: P1)

[Source: .specify/archive/002-sqlite-storage/spec.md -> User Story 1]

An operator starts `whatsapp-mcp serve` (or `stdio`, or `pair`) for the first time on an empty data directory. A
single database file appears in the data directory. It will hold both the linked device's keys and the message
history. On every later start the same file is used, from any working directory.

**Why this priority**: S1 and Principle III require one file for all state, and every later feature stores into it.
It also removes the old split between two files (§6 #3).

**Independent Test**: Start a long-running command on an empty data directory, stop it, and check that exactly one
database file (plus its journal files) exists and contains both the device store tables and the message tables.
Start again and check that nothing is recreated.

**Acceptance Scenarios**:

1. **Given** an empty data directory, **When** `stdio`, `serve` or `pair` starts, **Then** `<data dir>/whatsapp.db`
   is created, holds both schemas at their latest supported versions, and the directory, database, sidecars and
   sensitive temporary files meet FR-037 and FR-073 on that platform, including Windows ACLs.
2. **Given** an existing, up-to-date database, **When** a long-running command starts again, **Then** it opens the
   same file without changing the schema, and all stored chats and messages are still there.
3. **Given** a file at `<data dir>/whatsapp.db` that is not a database (for example truncated or random bytes),
   **When** a long-running command starts, **Then** it fails with a message that names the file and says it is not a
   usable database, and the file is left untouched.
4. **Given** a data directory with old `store/whatsapp.db` or `store/messages.db` files from the previous bridge,
   **When** a command starts, **Then** they are ignored (D16).
5. **Given** an existing directory, database or sidecar with unsafe ownership/access or a substituted path,
   **When** a command starts, **Then** startup fails before database writes, names the unsafe artifact safely, and
   does not silently repair permissions, truncate a target or replace the database.

---

### User Story 8 - The schema evolves safely (Priority: P1)

[Source: .specify/archive/002-sqlite-storage/spec.md -> User Story 2]

A developer adds a schema change in a later feature as a new numbered migration that ships inside the binary. An
operator upgrades the binary, and the database is brought up to date on the next start without any manual step.
If a migration fails, that step is rolled back and startup stops. Earlier successful steps may remain committed;
the operator is told which steps completed and how to follow the recovery procedure.

**Why this priority**: S2 and Principle III. Without a migration history, every schema change after the first
release would be a manual upgrade or a data loss.

**Independent Test**: Open a database at each earlier version (built by applying only the first migrations) and check
that opening it applies the rest; inject failures in both schema histories and check their transaction boundaries;
mark either schema with a future version and check read-only refusal, including the main file and sidecars.

**Acceptance Scenarios**:

1. **Given** an empty database, **When** it is opened, **Then** every message-schema migration is applied in order,
   and the recorded version equals the latest one the binary knows.
2. **Given** a database at an earlier version, **When** it is opened, **Then** only the missing migrations are
   applied, in order, and existing data is kept.
3. **Given** a migration that fails part-way, **When** the database is opened, **Then** opening fails with a message
   naming the migration, and the schema and data are exactly as before that migration.
4. **Given** a database whose message or device schema is at a version newer than the binary supports, **When** it
   is opened, **Then** preflight fails naming the schema and both versions, neither schema is upgraded, and the main
   file and sidecars remain unchanged as defined in FR-043.
5. **Given** the device store's own schema is behind the whatsmeow version in the binary, **When** the database is
   opened, **Then** the device store schema is upgraded as well.
6. **Given** a supported device upgrade that succeeds followed by a failing message migration, **When** opening
   fails, **Then** the device upgrade and any earlier committed message migrations remain, the failing step leaves
   no partial schema/data/version, no usable store is returned, and an offline retry follows the documented rules.
7. **Given** a failing device upgrade, **When** opening fails, **Then** no message migration runs, the failed device
   step meets its documented atomicity guarantee, and neither ingest nor WhatsApp connection starts.

---

### User Story 9 - Ingest stores chats and messages correctly in any order (Priority: P1)

[Source: .specify/archive/002-sqlite-storage/spec.md -> User Story 3]

Feature 004 hands the store live messages and history-sync batches, which arrive in no particular order and may
repeat. Whatever the order, the stored chats show their real latest activity and the best name known, and each
message is stored once.

An account owner receives or sends supported messages while the service is connected and can rely on their content and identity being available for later use. [Source: .specify/archive/005-message-ingest/spec.md -> User Story 1]

After linking a device, the owner wants the history supplied by WhatsApp to be retained alongside new messages, without old history making a chat appear less recent. [Source: .specify/archive/005-message-ingest/spec.md -> User Story 2]

**Why this priority**: §6 #10 (time going backwards) and §6 #9's effect on names break chat lists and the "last
message" shown to the user. S3, S4, D18.

Keep Live Messages (specs/005-message-ingest): Live messages are the basic ongoing record.

Recover Available History (specs/005-message-ingest): History makes a fresh installation useful and must coexist with live delivery.

**Independent Test**: Store a fixed set of chats and messages in several different orders, including duplicates, and
check that the resulting chats and messages are identical every time. Re-stores of one `(chat_jid, id)` with
different content are checked separately: the copy written last is kept.

Keep Live Messages: Deliver synthetic live events to a connected test session and inspect stored records, without a real account.

Recover Available History: Feed synthetic history conversations and live messages in varied orders and compare the resulting records.

**Acceptance Scenarios**:

1. **Given** a message `(chat C, id X)` already stored, **When** the same `(C, X)` is stored again with different
   content, **Then** there is still exactly one message `(C, X)`, and it carries the new content. **Given** a stored
   media message with a URL and direct path, **When** a copy without them is stored, **Then** the stored URL and
   direct path are kept and the other fields come from the new copy.
2. **Given** the same message ID `X` in two different chats, **When** both are stored, **Then** both are kept as
   separate messages.
3. **Given** chat C with a latest message at 12:00, **When** a history message from 09:00 is stored in C, **Then**
   C's last activity time stays 12:00.
4. **Given** chat C with last activity 09:00, **When** a message from 12:00 is stored, **Then** C's last activity
   time becomes 12:00.
5. **Given** a message for a chat that is not stored yet, **When** it is stored, **Then** the chat is created with
   that message's time as its last activity, and the message is stored, as one atomic step.
6. **Given** a chat whose stored name is the real name "Alice", **When** an update offers the fallback name
   "491511234567", **Then** the name stays "Alice".
7. **Given** a chat whose stored name is a fallback, **When** an update offers a real name, **Then** the real name is
   stored. **Given** a real name seen at 10:00, **When** a different real name seen at 11:00 is offered, **Then** the
   new name is stored; **When** one seen at 09:00 is offered instead, **Then** the stored name stays.
   **Given** a stored saved name "Mum" (from the address book), **When** a push name "Bank Support" seen later is
   offered, **Then** the name stays "Mum".
8. **Given** an image, video or document message with a caption, **When** it is stored, **Then** the caption is kept
   in its own field, separate from the message text, and the media metadata needed to download it later is kept.
9. **Given** a message from `491511234567:12@s.whatsapp.net` (a device of that user), **When** it is stored, **Then**
   the sender is kept as the user's full JID `491511234567@s.whatsapp.net`, and its normalised phone number as
   `491511234567`. **Given** a sender that is an unresolved LID, **Then** the full LID JID is kept and the phone
   number is empty.

10. **Given** a valid text message, **When** it is received or sent by the account, **Then** its chat, message ID, sender, time, direction and text are stored.
11. **Given** an image, video, audio or document message, **When** it is ingested, **Then** its type and available download metadata are stored; image, video and document captions are kept separately from text.
12. **Given** the same chat and message ID delivered twice, **When** both copies are stored, **Then** one message remains and the existing store's replacement and non-empty media preservation rules apply.
13. **Given** a message without supported text or recognized media, **When** it arrives, **Then** it is ignored without creating a message.

14. **Given** a history sync containing supported messages, **When** it is processed, **Then** each valid message retains its own time, sender, direction, text, caption and available media metadata under the correct chat.
15. **Given** a newer live message already stored, **When** older history is processed, **Then** the chat's last activity remains the newer time.
16. **Given** history larger than the store's batch limits, **When** it is processed, **Then** it is divided into bounded batches; a failed batch is reported and no partial batch is claimed as saved.
17. **Given** a history conversation with no usable message timestamp, **When** it is processed, **Then** no fabricated message time is stored; invalid messages are reported or skipped without preventing independent valid messages from being processed.

---

### User Story 10 - The tools find chats, contacts and messages (Priority: P1)

[Source: .specify/archive/002-sqlite-storage/spec.md -> User Story 4]

Features 005 and 006 answer the 14 MCP tools by calling store queries, and only wire them up. Every query a tool
needs exists, is correct for the cases where the old bridge was wrong, and is fast on a large history.

**Why this priority**: the roadmap's "done when" for this feature, and §6 #2, #3, #6, #7, #8.

**Independent Test**: Seed a database with known chats, contacts and messages in a temporary directory and check every
query's result against the expected rows.

**Acceptance Scenarios**:

1. **Given** seeded chats, **When** chats are listed with a search text, a page size and an offset, sorted by last
   activity or by name, **Then** exactly the chats whose name or JID contains the text are returned, in that order,
   each optionally with its real latest message.
2. **Given** a chat JID, **When** the chat is requested, **Then** it is returned with its latest message, or a
   distinct "not found" result if no such chat exists.
3. **Given** contacts in the device store, **When** contacts are searched, **Then** the text matches full name, push
   name, first name, business name or phone number; groups are excluded; and a contact known under both a LID and a
   phone number appears once.
4. **Given** a contact given as a phone number, a phone JID or a LID, **When** the direct chat with that contact is
   requested, **Then** only the chat whose phone number equals the contact's number exactly is returned (never a
   partial match), else the contact's `@lid` chat if it has not been merged yet, or "not found".
5. **Given** a contact who has a direct chat and has written in two groups, one of them under a LID that the device
   store maps to the contact's phone number, **When** the contact's chats are requested by phone number or by LID,
   **Then** those three chats are returned once each, newest activity first, each with its real latest message.
6. **Given** seeded messages, **When** messages are listed with any combination of after, before, chat, sender
   (a phone number, phone JID or LID) and search text, with a page size and offset, **Then** exactly the matching
   messages are returned, newest first; the sender matches messages stored under either of the contact's
   identities, and the search text matches either the message text or the caption.
7. **Given** a listing with context requested, **When** N before and M after are asked for, **Then** each hit comes
   with up to N earlier and M later messages from the same chat.
8. **Given** a contact given as a phone number, a phone JID or a LID, **When** the last interaction is requested,
   **Then** the newest message in the direct chat with them (from either side) or sent by them in a group, under
   either identity, is returned, or nothing.
9. **Given** every message returned by any query, **Then** it carries its ID, chat JID, chat name, sender JID, sender
   phone number, sender name, time, whether it is from me, text, caption and media type.

---

### User Story 11 - A single message is found by chat and ID (Priority: P2)

[Source: .specify/archive/002-sqlite-storage/spec.md -> User Story 5]

Tools that act on one message (context, media download) identify it by `(chat_jid, id)`. When a caller gives only
the ID and it exists in several chats, it gets an error that lists those chats, so it can ask again precisely.

**Why this priority**: S6 and §6 #14. Message IDs are unique only within a chat.

**Independent Test**: Store the same ID in two chats and one other ID in one chat, then look each up by
`(chat_jid, id)` and by ID alone.

**Acceptance Scenarios**:

1. **Given** message X in chat C, **When** it is looked up by `(C, X)`, **Then** that message is returned.
2. **Given** no message `(C, X)`, **When** it is looked up, **Then** the result is a distinct "not found" naming the
   chat and the ID.
3. **Given** ID X in exactly one chat, **When** it is looked up by ID alone, **Then** that message is returned.
4. **Given** ID X in chats C1 and C2, **When** it is looked up by ID alone, **Then** the result is a distinct
   "ambiguous ID" error that lists C1 and C2.
5. **Given** a media message, **When** its media metadata is requested by `(chat_jid, id)`, **Then** everything
   needed to download it later is returned, or "not a media message" for a message without media.

---

### User Story 12 - Merge a LID chat into the phone-number chat (Priority: P2)

[Source: .specify/archive/002-sqlite-storage/spec.md -> User Story 6]

At startup, and whenever a new LID-to-phone mapping arrives, feature 004 asks the store to merge that LID into its
phone number. Afterwards there is one chat per person, holding all their messages, and every message they sent in
any chat carries their phone JID.

An owner sees a person's direct chat and group messages consistently associated with their phone identity when WhatsApp supplies a mapping, while unresolved identities remain usable. [Source: .specify/archive/005-message-ingest/spec.md -> User Story 3]

**Why this priority**: P2 keeps the startup merge (§3.5). The store provides the operation; deciding which chats to
merge is 004's job.

Unify Contact Identities (specs/005-message-ingest argued P1): Split identities hide messages and misidentify senders.

**Independent Test**: Seed a LID chat and a phone-number chat with overlapping and distinct messages, merge, and check
the result; run the merge again and check nothing changes.

Unify Contact Identities: Seed synthetic LID and phone identities and overlapping messages; run startup reconciliation and ingest events with and without mappings.

**Acceptance Scenarios**:

1. **Given** a LID chat L and a phone-number chat P, **When** L is merged into P, **Then** all of L's messages belong
   to P, L no longer exists, P's last activity is the later of the two, and P's name is the better of the two by the
   name rule (higher quality, then newer name time, then the byte-wise greater name). L's name takes part only if
   it is saved or real: a fallback name of L is never carried over, even when P's own fallback is older.
2. **Given** only L exists, **When** it is merged into P, **Then** P is created with L's time and messages, and with
   L's name if it is saved or real; if L only had a fallback name, P is created without a name.
3. **Given** L and P both hold message ID X, **When** L is merged, **Then** P keeps its own copy of X, L's copy is
   dropped, and the merge completes.
4. **Given** messages in L, and in two group chats, whose sender is the LID, **When** the LID is merged, **Then**
   their sender becomes the phone JID and their phone number is filled in, in every chat.
5. **Given** no chat L but group messages sent under the LID, **When** the LID is merged, **Then** those senders
   are rewritten and no chat is created or removed.
6. **Given** a merge that fails part-way, **Then** every affected chat and message is exactly as before it.
7. **Given** a merge already done, **When** it is requested again, **Then** nothing changes and no error is
   reported.

8. **Given** a mapped LID on a chat or sender, **When** a message is ingested, **Then** the mapped phone JID is used and the sender's phone number is retained.
9. **Given** an unmapped LID, **When** its message is ingested, **Then** the LID is retained and its sender phone number is empty, rather than guessed.
10. **Given** a mapped LID chat and a phone chat with overlapping IDs, **When** startup reconciliation runs, **Then** the store's atomic merge retains the phone chat's duplicate, moves other messages, applies the existing name and latest-time rules, and rewrites that sender in all chats; repeating it changes nothing.
11. **Given** an unmapped LID or a failed mapping or merge, **When** reconciliation runs, **Then** existing data is not deleted; the outcome is reported safely and a later startup can retry.
12. **Given** messages stored under a LID, **When** its phone mapping is detected during a running session, **Then** the same atomic merge is attempted without waiting for restart.

---

### User Story 13 - Storage remains private, bounded and recoverable (Priority: P1)

[Source: .specify/archive/002-sqlite-storage/spec.md -> User Story 7]

The operator can keep the linked-device credentials and history private, diagnose failures without exposing
secrets, and recover a consistent database without connecting a copied device store to WhatsApp.

**Why this priority**: the file contains credentials as well as conversations. Resource exhaustion, unsafe paths
and incomplete recovery procedures must be addressed before later features depend on this store.

**Independent Test**: Use synthetic device/message data, isolated temporary directories, injected failures and
separate OS test identities. No real WhatsApp account or network connection is needed.

**Acceptance Scenarios**:

1. **Given** newly created and preexisting storage, including WAL/SHM and temporary files, **When** an unrelated
   identity attempts access on Unix and Windows, **Then** it cannot read or modify sensitive artifacts. Link swaps,
   junctions/reparse points, hard links and non-regular files are rejected according to FR-073.
2. **Given** an input, batch, result/context request or concurrent workload at and beyond each approved bound,
   **When** the store is called directly, **Then** boundary values work and excess work fails predictably without
   partial writes, unbounded allocation, silent truncation or an unbounded queue. Cancellation releases resources
   within the approved allowance, including while waiting for a database lock.
3. **Given** SQL-like search text, an unsupported sort, malformed identifiers/media metadata or overflowing
   numeric arguments, **When** a store operation runs, **Then** values cannot change query structure, invalid
   arguments are rejected without mutation, and valid literal search text still matches literally.
4. **Given** disk-low, disk-full, read-only storage or a migration failure, **When** an operation fails, **Then**
   committed state remains usable for documented recovery, no keys/history are deleted to make space, and logs
   contain the required safe event fields but no secret material or message bodies at any log level.
5. **Given** a write workload stopped using the documented quiesced-backup procedure, **When** its protected backup
   is restored to an isolated directory and opened offline, **Then** integrity checks pass and both schemas and all
   writes committed before quiescence are preserved. The original and restored device stores never connect.
6. **Given** the operator guide, **When** the operator reviews retention and decommissioning, **Then** storage
   growth, encryption/backup responsibilities, remaining copies and secure-deletion limitations are explicit.

---

### User Story 14 - Link a Device and Reuse Its Session (Priority: P1)

[Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> User Story 1]

As the account owner, I can link a device and reuse its saved session after an eligible quiet stop. If the marker stays unsafe, I must complete offline recovery before reconnecting.

**Why this priority**: A durable connection is the prerequisite for every later WhatsApp capability.

**Independent Test**: Use isolated synthetic storage to confirm linking and orderly resource drain. Restart after an eligible quiet stop and verify reuse without a QR. Repeat with a stop that leaves the marker unsafe and verify restart blocks reuse. Manual checks require separate permission. [Source: .specify/archive/004-shutdown-drain/spec.md -> User Story 1] [Source: .specify/archive/004-shutdown-drain/spec.md -> User Story 2]

**Acceptance Scenarios**:

1. **Given** an unpaired data directory, **When** the owner runs `pair`, **Then** the current QR is displayed in the terminal and the command waits for success, expiry, cancellation or a connection failure.
2. **Given** an active pairing attempt, **When** the phone successfully links the device and connection is confirmed, **Then** the connection owner's in-process status snapshot reports connected and logged in, the QR is cleared, the session is saved, and `pair` exits successfully after orderly shutdown.
3. **Given** a saved session after a production Connect attempt, **When** a connection-owning command starts, **Then** it may reuse the identity if the previous stop cleared the marker under FR-110. If the marker is still unsafe, it refuses automatic connection and QR and requires offline recovery.
4. **Given** another process owns the data directory, **When** `pair` starts, **Then** it fails with the existing lock-holder diagnostic and neither creates a second client nor modifies the owner's session.
5. **Given** an active connection or pairing attempt, **When** the owner interrupts the command, **Then** connection work and device writes stop before storage closes and the data-directory lock is released.
6. **Given** either existing `stdio` or `serve` shell, **When** it starts with a clean marker and saved session, **Then** it connects and remains running until cancellation; unsafe state blocks startup account work. With no saved session it remains unpaired without a QR. Neither shell exposes a network listener; both drain before releasing ownership. The feature 003 shells had no tools; since feature 006, `stdio` exposes the five local read-only tools in FR-140. `serve` still has no MCP endpoint. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> FR-002]
7. **Given** a saved-session startup attempt fails or times out in `stdio` or `serve`, **When** safe retries within the same active generation are demonstrated and no terminal event or 405 handoff intervenes, **Then** at most three additional attempts follow five cancellable seconds each. Otherwise retries are temporarily unavailable: remain disconnected with a diagnostic, preserve the identity and open no pairing window. Never connect after retirement of an unproven generation.
8. **Given** a fresh device and an explicitly approved QR pairing attempt, **When** the registration payload and QR client type are prepared, **Then** they identify the companion as Linux/Chrome as the previous service did, without modifying global library state or saved credentials. The phone's displayed label is observed separately and is not guaranteed by this application.

---

### User Story 15 - Recover Pairing Without Restarting (Priority: P1)

[Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> User Story 2]

As the account owner, I can receive replacement QR codes within an active attempt. After a production Connect attempt, logout or timeout requires offline recovery before a separately approved fresh pairing.

**Why this priority**: Recoverability fixes the existing failure that leaves a running service permanently unpaired.

**Independent Test**: Simulate QR replacement, logout and a new request; prove the latter is denied after a production Connect attempt, and exercise separately approved offline recovery without losing conversations.

**Acceptance Scenarios**:

1. **Given** a fresh pairing attempt reaches final timeout without durable identity and current-generation Connected, **When** the owner presses Enter, **Then** no new attempt or QR starts; the owner retires the attempt and requires explicit offline recovery before a separate new pairing.
2. **Given** a running session receives a logout event, **When** its in-process status snapshot is inspected, **Then** connected and logged-in are false and no previous QR is available; logout alone does not open a new pairing window.
3. **Given** that logged-out process has attempted production Connect, **When** a new pairing is requested, **Then** it is denied even after cleanup; offline recovery and a separately approved fresh pairing are required, preserving conversations.
4. **Given** pairing is already in progress, **When** concurrent requests ask for a QR, **Then** they share the current unexpired attempt rather than starting competing connections.
5. **Given** an old attempt completes after it was cancelled or superseded, **When** its delayed events arrive, **Then** they cannot replace the current QR or incorrectly mark the new session as connected.
6. **Given** an active owner-approved attempt, **When** WhatsApp supplies a replacement QR, **Then** it replaces the previous code automatically without another approval and only the current unexpired code is available. Expiry of one code alone does not authorize starting another attempt.
7. **Given** an unresolved final timeout, **When** the owner cancels, presses Enter or input ends, **Then** `pair` exits unsuccessfully after orderly shutdown with the marker unsafe and starts no further attempt.
8. **Given** interrupted app-state/contact sync and an unsafe run, **When** separately authorized offline verification and any required unlink and local identity removal complete, followed by a separately authorized fresh pair, **Then** transferred repair bits are materialized for the new identity before ordinary progress. Bounded repair preserves chats and the new identity's credentials/session keys, clears only successfully repaired state and retains failed or superseded needs. Identity removal may remove the old device's keys only as FR-099 permits.
9. **Given** an unsafe-run marker after a crash or failed logout cleanup, **When** the process restarts, **Then** it starts no automatic connection, pairing or QR and reports that offline operator recovery is required, even if a saved identity exists or no identity was saved before the crash.
10. **Given** that unresolved marker, **When** an operator explicitly selects recovery in `pair`, verifies the named account/device and separately authorizes any required phone-side unlink and local identity removal, **Then** local recovery preserves chats/messages and remains offline; a fresh pairing needs a separate explicit approval. EOF, refusal, insufficient verification or a definite failed commit preserves the block; an ambiguous commit exits unsuccessfully and requires committed-state reinspection before further action.

---

### User Story 16 - Understand Local Session Status (Priority: P2)

[Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> User Story 3]

As an operator, I can run `whatsapp-mcp status` to inspect whether a local session exists and which version is available, without accidentally starting pairing or waiting for a network connection.

**Why this priority**: Operators need an honest, quick diagnostic that does not change account state.

**Independent Test**: Run `status` against missing, unpaired, paired, unsafe and locked temporary data directories with network access prohibited.

**Acceptance Scenarios**:

1. **Given** no data directory or database/sidecar artifacts exist, **When** `status` runs, **Then** it reports not connected, not logged in, pairing-required false and the bundled version labelled as not refreshed, exits successfully, and creates no data directory, database or lock file; an existing held lock still causes the holder error even before its owner creates the database.
2. **Given** a saved session and no running owner, **When** `status` runs, **Then** it reports a local snapshot with connected false, logged-in true based on saved credentials, pairing-required false, and the bundled version labelled as not refreshed; it explicitly says the saved session has not been verified with WhatsApp.
3. **Given** another process owns the data directory, **When** `status` runs, **Then** it exits with a clear locked-data-directory error naming the holder rather than reading concurrently or claiming to know that process's live connection state.
4. **Given** unsafe, corrupt or unsupported existing storage, **When** `status` runs, **Then** it reports an actionable error without repairing, creating or migrating that storage.
5. **Given** an unsafe-run marker or a legacy database without a marker, **When** `status` inspects it, **Then** it warns that offline recovery is required and exits 1; if the database is otherwise inspectable, its four fields still describe local identity presence rather than claiming account verification.

---

### User Story 17 - Stay Compatible With WhatsApp (Priority: P2)

[Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> User Story 4]

As an operator, I can keep a connection running while the application refreshes its WhatsApp Web version and makes a limited recovery attempt if WhatsApp rejects an outdated client.

**Why this priority**: Compatibility updates should not require routine operator intervention or cause endless reconnect loops.

**Independent Test**: Simulate time, version responses and outdated-client events, and inspect version changes, retry counts and resulting status without contacting WhatsApp.

**Acceptance Scenarios**:

1. **Given** a connection-owning command starts, **When** startup version discovery succeeds, **Then** the discovered version is selected before its first connection attempt.
2. **Given** discovery fails, **When** the command starts, **Then** a warning is reported and connection proceeds with the last usable version, or the bundled version if none has been discovered.
3. **Given** a connected session, **When** six hours elapse and refresh succeeds, **Then** the selected version changes for the next connection without disconnecting the current session.
4. **Given** WhatsApp reports an outdated client (405), **When** same-active-generation recovery is demonstrated safe, **Then** retries wait five seconds and refresh before connecting, at most three. Otherwise recovery is temporarily unavailable and the owner remains disconnected; retirement of an unproven generation never permits a replacement Connect.
5. **Given** the 405 budget is exhausted or safe same-generation retry is unproven, **When** more outdated-client events arrive, **Then** no further automatic retry occurs and an actionable diagnostic is reported; only a confirmed successful connection resets the budget.

---

### User Story 18 - Restart Without Offline Recovery After a Clean Stop (Priority: P1)

As the account owner, I stop `stdio` or `serve` normally and start it again later. The saved session connects again without an offline recovery and without a new pairing, provided the program could prove that nothing from the previous connection was still running when it stopped.

**Why this priority**: Today every run that connects to WhatsApp leaves the marker unsafe, so every restart needs an offline recovery and a fresh pairing. That makes a long-running server unusable and blocks the real-account history sync of message ingest (feature 005).

**Independent Test**: On the simulator, connect a saved session, stop it normally with no library work left, and restart: the second run connects without recovery. Repeat with leftover work, a refused call and an incomplete observation; each keeps the marker unsafe and the restart refuses to connect.

**Acceptance Scenarios**:

1. **Given** a connected session with no library work left at stop, **When** the owner stops the command normally, **Then** the program blocks network traffic, then storage, observes that no library work remains and that no call was refused, clears the marker, closes storage and releases the lock, and reports that the session can be reused.
2. **Given** that clean stop, **When** `stdio` or `serve` starts again, **Then** it connects with the saved session, opens no pairing window and needs no offline recovery.
3. **Given** library work that is still running when the observation deadline passes, **When** the command stops, **Then** the marker stays unsafe, storage still drains and closes in order, and the next start requires offline recovery as today.
4. **Given** a call to storage or the network that was refused after its block, **When** the check runs, **Then** it fails, whatever the observation shows, and the marker stays unsafe.
5. **Given** a confirmed logout or a request to delete the client during the run, **When** the stop passes the check, **Then** the marker clears only if the logout cleanup has been made durable, as today; a passing check never overrides a logout.
6. **Given** a crash, forced kill or second stop signal before safe clear, **Then** no orderly clear is made. An orderly stop that reaches the clear decision after its bound fails the check (`stop_bound_exceeded`) and leaves the marker unsafe. If the check passed within the bound but subsequent ordered cleanup times out, the command still exits unsuccessfully; the in-bound clear decision may leave a clean marker. See FR-096.
7. **Given** a run that never attempted a production connection, **When** it stops normally, **Then** the existing safe-clear rules apply unchanged.
8. **Given** the late-logout ordering that caused the 2026-11-04 fail-closed policy (a logout handled by the client library after retirement, finding its callback removed and its delete refused; regression `TestRunGuardLatePinnedLogoutAfterRetirement`), **When** the stop is checked, **Then** the check fails, either because the logout work is still running at the deadline or because its delete was refused, and the marker stays unsafe. A clean marker is never committed before that work has finished.
9. **Given** a client-library message handler that is still running (held) when its generation is retired, **When** the check runs, **Then** it fails until the handler has returned, and passes only if the handler made no call after the blocks.

[Source: .specify/archive/004-shutdown-drain/spec.md -> User Story 1]

The general refusal and timeout wording in this story is subject to FR-109’s two counted-but-non-failing refusal cases and FR-096’s distinction between a late decision and an in-bound pass followed by a lifecycle timeout. [Source: .specify/archive/004-shutdown-drain/spec.md -> FR-002] [Source: .specify/archive/004-shutdown-drain/spec.md -> FR-011]

---

### User Story 19 - Pair Once and Keep the Device (Priority: P1)

As the account owner, I run `pair`, scan the QR code once, and the device stays linked and reusable. The connection that WhatsApp asks for right after pairing no longer runs on the client that did the pairing, so late traffic from the pairing session cannot reach the new connection.

**Why this priority**: Today the post-pair reconnect reuses the pairing client. Late work from the pairing session can send on the new socket, and the run still ends with the marker unsafe, so the newly linked device needs an offline recovery before first use.

**Independent Test**: On the simulator, pair, receive the restart request that follows pairing, and confirm that the pairing session's late sends never reach the new connection, that the new connection uses a fresh client and generation, and that a clean stop leaves the marker clear for the next `stdio` start.

**Acceptance Scenarios**:

1. **Given** a successful pairing followed by WhatsApp's restart request, **When** the pairing generation passes the quiet-stop check, **Then** the program starts exactly one connection on a fresh client and generation, loaded from the durably saved identity, under the single attempt slot and the 30-second watchdog.
2. **Given** that the fresh connection confirms, **When** `pair` stops normally and its own stop passes the check, **Then** `pair` exits successfully, the marker is clear and the next `stdio` or `serve` start reuses the session.
3. **Given** that the fresh connection confirms but `pair`'s own stop fails the check, **When** `pair` ends, **Then** it exits 0 with the existing "linked, offline recovery is required before the next run" notice and the marker unsafe, as the owner decided for feature 003 (design review 516648b). A storage fault or any other stop error still exits 1.
4. **Given** the restart request arrives before the pairing success has been handled (observed on the simulator in feature 003), **When** the owner receives it, **Then** it waits for the pairing success within the 30-second watchdog, as today; a pairing error, logout, stop or the watchdog refuses it and no connection starts.
5. **Given** the pairing generation fails the check, **When** the restart request arrives, **Then** no further connection starts; `pair` reports an incomplete link that needs offline recovery, keeps the marker unsafe and exits unsuccessfully.
6. **Given** the fresh connection fails, stalls past its watchdog or is cancelled, **When** `pair` ends, **Then** the failure is final: no second reconnect, an incomplete-link report, the marker unsafe and an unsuccessful exit.
7. **Given** late work from the pairing generation (a session announcement, a receipt, a key share, a resent request or a disconnect), **When** it tries to send or write after its generation's blocks, **Then** it is refused and never reaches the fresh connection. A new dial, or a call to the session, counts against the pairing generation's check. A write on a connection that the block already closed sends nothing and is counted, but does not fail the check.

[Source: .specify/archive/004-shutdown-drain/spec.md -> User Story 2]

---

### User Story 20 - Know Why the Session Cannot Be Reused (Priority: P2)

As an operator, when a stop does not pass the check, I can see the reason in plain words and know that offline recovery is needed. Over several runs I can see how often stops pass, which decides whether the fork fallback is needed.

**Why this priority**: A failed check must not look like a crash, and the owner needs a measured pass rate to judge option A against the fork.

**Independent Test**: Force each failure category on the simulator and read the stop diagnostic and the `status` output. No diagnostic may contain library state, arguments, keys or account identifiers.

**Acceptance Scenarios**:

1. **Given** a stop that fails the check, **When** it finishes, **Then** stderr reports one failure category (for example, work still running, a refused network call, a refused storage call, an incomplete observation or a deadline), the counts involved and that offline recovery is required, without raw observation data.
2. **Given** a stop that passes, **When** it finishes, **Then** stderr reports that the session can be reused.
3. **Given** either outcome, **When** `status` runs afterwards, **Then** it reports the marker state as it does today; nothing new is stored beyond the marker.

[Source: .specify/archive/004-shutdown-drain/spec.md -> User Story 3]

---

### User Story 21 - Recognize Chats Without Repeated Lookups (Priority: P2)

An owner sees useful chat names without each group message triggering another group-information request.

**Why this priority**: Stable names improve recognition and reduce unnecessary network work.

**Independent Test**: Supply stored names, synthetic contact and history names, and a counted group-information provider while ingesting repeated messages.

**Acceptance Scenarios**:

1. **Given** a stored non-empty chat name, **When** a message arrives, **Then** it is consulted first and a worse fallback never replaces it.
2. **Given** a group lacking a stored name, **When** multiple messages need its name, **Then** available history names are used before a group-information lookup and successful lookup results are cached rather than fetched per message.
3. **Given** unavailable contact or group information, **When** a name is needed, **Then** the existing fallback and name-quality rules apply and the message is not discarded solely for lack of a name.

[Source: .specify/archive/005-message-ingest/spec.md -> User Story 4]

---

### User Story 22 - Browse Stored Conversations (Priority: P1)

[Source: .specify/archive/006-mcp-stdio-tools/spec.md -> User Story 1]

As the account owner using a local assistant, I can discover, search and inspect my stored chats without running a separate daemon or confusing an absent conversation with a broken service.

**Why this priority**: Browsing conversations is the first useful read capability and establishes the common calling and result conventions.

**Independent Test**: Start a local client with synthetic conversations and a fake WhatsApp connection, discover the two chat tools and browse multiple pages, including chats with no messages. Exact five-tool discovery is the cross-story feature acceptance below, not a prerequisite for the US1 development checkpoint.

**Acceptance Scenarios**:

1. **Given** an unlocked, valid data directory at the US1 development checkpoint, **When** the client starts `whatsapp-mcp stdio` and initializes MCP, **Then** it discovers `list_chats` and `get_chat` with their required inputs and real defaults; stdout contains protocol traffic only. This two-tool checkpoint is not a complete feature 006.
2. **Given** 35 stored chats, **When** `list_chats` is called without optional inputs, **Then** it returns the first 30 in most-recent-activity order; page 1 returns the remaining five with no duplicates for unchanged data.
3. **Given** matching chat names and JIDs, **When** a query and `sort_by: "name"` are supplied, **Then** only matching chats are returned in name order; equal sort values have a stable tie-breaker.
4. **Given** a chat with a known latest message, **When** it is listed or fetched with `include_last_message` true and then false, **Then** the latest-message detail appears only in the former, while the chat's identity and last-activity time remain correct.
5. **Given** an unknown `chat_jid`, **When** `get_chat` is called, **Then** it returns a clear not-found tool error; an empty search instead returns a successful empty list.
6. **Given** another process holds the data directory, **When** stdio starts, **Then** it fails with the existing lock-holder diagnostic without opening a second account owner or silently proxying.
7. **Given** the owner launched the local stdio service, **When** its client reads any stored chat or searches contacts through the exposed tools, **Then** no per-chat allow-list or per-read approval is required; this read permission does not grant pairing or sending permission.

---

### User Story 23 - Find a Contact and Their Conversations (Priority: P1)

[Source: .specify/archive/006-mcp-stdio-tools/spec.md -> User Story 2]

As the account owner, I can find someone by their saved or advertised name or phone number and inspect their direct chat and group conversations without partial-number mistakes or repeated chats.

**Why this priority**: Reliable identity matching prevents showing the wrong person's conversation and fixes several historical lookup defects.

**Independent Test**: Seed contacts with distinct full, push, first and business names, mapped and unmapped LIDs, similar phone numbers, and repeated messages in shared groups. Call the three contact tools without any live lookup.

**Acceptance Scenarios**:

1. **Given** contacts whose full name, push name, first name, business name or phone number matches a query, **When** `search_contacts` is called, **Then** each matching contact appears once and groups do not appear as contacts.
2. **Given** a phone number with supported formatting and its equivalent phone JID and mapped LID, **When** each is supplied to `get_direct_chat_by_contact` or `get_contact_chats`, **Then** each resolves to the same person and equivalent stored results.
3. **Given** two contacts with overlapping phone digits, **When** the complete number of one is requested, **Then** only its exact direct chat matches; a substring cannot select the other contact.
4. **Given** a contact has many messages in a group and a newer last message from another participant, **When** `get_contact_chats` is called, **Then** that group appears once with its actual latest message, not an arbitrary message from the contact.
5. **Given** an unmapped LID with a stored direct chat, **When** that LID is requested, **Then** the existing LID identity can be matched without inventing a phone number or making a network request.
6. **Given** no matching contact chats, **When** `get_contact_chats` is called, **Then** it returns a successful empty list; a missing direct chat is a not-found tool error.

### Cross-Story Feature Acceptance

1. **Given** both stories are implemented, **When** the client initializes MCP and discovers tools, **Then** it discovers exactly the five tools in FR-140 with their true defaults and input/output schemas; neither deferred login tool is registered or callable, and stdout contains protocol traffic only.
2. **Given** a mapped phone/LID pair and only its LID direct-chat row, **When** either equivalent identity is supplied to the singular contact-chat tool, **Then** it returns that exact mapped LID chat. If both direct rows exist, the phone row wins; plural results preserve both distinct stored chat identities.

[Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "Cross-Story Feature Acceptance"]

### Edge Cases

- **`version` with a broken environment:** it reads no configuration, touches no data directory and takes no
  lock, so it works even when everything else is misconfigured. [Source: .specify/archive/001-binary-skeleton/spec.md -> "`version` with a broken environment"]

- **Unknown flag or a flag that does not belong to the command:** usage error with the usage exit code, naming the
  flag. [Source: .specify/archive/001-binary-skeleton/spec.md -> "Unknown flag or a flag that does not belong to the command"]

- **Empty environment variable** (`WHATSAPP_MCP_LOG_LEVEL=`): treated as unset, so the next source applies.
  Claude Desktop and service files often carry empty entries. [Source: .specify/archive/001-binary-skeleton/spec.md -> "Empty environment variable"]

- **Unknown `WHATSAPP_MCP_*` environment variable** (for example a typo `WHATSAPP_MCP_LOGLEVEL`): a warning naming
  it is logged, and startup continues. The environment is inherited and shared, so it is not treated as strictly
  as the config file, which the operator owns. [Source: .specify/archive/001-binary-skeleton/spec.md -> "Unknown `WHATSAPP_MCP_*` environment variable"]

- **Log level case:** `DEBUG` and `debug` are the same. `warning` is accepted as `warn`, as in §3.1. [Source: .specify/archive/001-binary-skeleton/spec.md -> "Log level case"]

- **`data_dir` in `config.toml`:** rejected with a message saying the data directory can only be set by flag or
  environment variable, because the file lives inside it. [Source: .specify/archive/001-binary-skeleton/spec.md -> "`data_dir` in `config.toml`"]

- **`config.toml` missing:** not an error; the defaults apply. **Present but unreadable** (permissions): an error
  naming the file. **Empty:** valid, the defaults apply. [Source: .specify/archive/001-binary-skeleton/spec.md -> "`config.toml` missing"]

- **Lock file left over from an earlier run:** its contents are ignored; only the OS lock counts. The new holder
  overwrites the PID. [Source: .specify/archive/001-binary-skeleton/spec.md -> "Lock file left over from an earlier run"]

- **Lock held but the PID cannot be read** (for example the holder is just starting): the message says another
  instance holds the data directory and that its PID is unknown. [Source: .specify/archive/001-binary-skeleton/spec.md -> "Lock held but the PID cannot be read"]

- **Signal before startup finishes:** the process still exits cleanly and releases anything it took. [Source: .specify/archive/001-binary-skeleton/spec.md -> "Signal before startup finishes"]

- **Database locked by a long write elsewhere in the process:** admitted work waits only within the configured
  busy/operation deadline and is cancellable. Normal-load concurrency meets SC-012; overload returns a distinct
  bounded busy/resource-limit result rather than retrying forever. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Database locked by a long write elsewhere in the process"]

- **Process killed mid-write:** on the next start every committed write is there and no partial write is visible. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Process killed mid-write"]

- **Disk full or read-only file:** the write fails with an error naming the file and the reason; nothing is half
  written. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Disk full or read-only file"]

- **Graceful shutdown:** closing stops admitting new operations at once, gives running ones a short grace period,
  then cancels them. A call made during or after closing gets a distinct "closed" error.
  [Source: .specify/archive/002-sqlite-storage/spec.md -> "Graceful shutdown"]
  Cancellation does not establish quiescence. If orderly shutdown exceeds the ten-second bound, report failure
  without closing the database or releasing the lock while work is active. See FR-029.
  [Source: .specify/archive/003-whatsapp-pairing-status/contracts/connection-owner.md -> "Stop and Failure Contract"]

- **Empty or missing message ID or chat JID, or a chat JID that is not a full JID:** the write is rejected with an
  error naming the field; nothing is stored. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Empty or missing message ID or chat JID, or a chat JID that is not a full JID"]

- **Message without a time:** rejected, because ordering and last activity depend on it. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Message without a time"]

- **Two messages in one chat with the same time:** the order is still deterministic (ties broken by ID), so paging
  never skips or repeats a message. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Two messages in one chat with the same time"]

- **Search text with `%`, `_`, quotes or a backslash:** matched literally. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Search text with `%`, `_`, quotes or a backslash"]

- **Search text in another script or case** ("ПРИВІТ" vs "привіт", "ÉTÉ" vs "été"): matches. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Search text in another script or case"]

- **Search text in another Unicode form** (composed `é` vs `e` plus a combining accent, in either the search text or
  the stored text): matches. **Search text without accents** (`ete` vs `été`): does not match. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Search text in another Unicode form"]

- **Phone number given as `+49 151 1234567`, `49-151-1234567` or `491511234567@s.whatsapp.net`:** all normalise to
  `491511234567` for phone-number filters and lookups. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Phone number given as `+49 151 1234567`, `49-151-1234567` or `491511234567@s.whatsapp.net`"]

- **Page beyond the end:** an empty result for a valid request within the approved offset/work limits. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Page beyond the end"]

- **Page size of zero or negative, negative/overflowing offset or context counts:** rejected; the store also
  enforces FR-076 limits independently of the tools (005, 006). [Source: .specify/archive/002-sqlite-storage/spec.md -> "Page size of zero or negative, negative/overflowing offset or context counts"]

- **Chat with no messages** (a history-sync conversation without stored messages): listed with no latest message;
  chats with no last activity time sort after all others by last activity. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Chat with no messages"]

- **Chats without a name** sort after named chats when sorted by name. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Chats without a name"]

- **`after` later than `before`:** an empty result, not an error. [Source: .specify/archive/002-sqlite-storage/spec.md -> "`after` later than `before`"]

- **Sender name for my own messages:** not looked up; the tools render them (006). [Source: .specify/archive/002-sqlite-storage/spec.md -> "Sender name for my own messages"]

- **Merging a chat into itself, or merging a chat that is not `@lid`:** rejected with an error. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Merging a chat into itself, or merging a chat that is not `@lid`"]

- **Device store after logout:** whatsmeow removes the device; the message tables are kept, and searching contacts
  returns no contacts until a device is paired again. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Device store after logout"]

- **Oversized history batch:** rejected atomically; the caller must submit bounded batches. Splitting policy belongs
  to ingest (004), not an unbounded internal buffer in this feature. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Oversized history batch"]

- **Ambiguous ID with too many matching chats or a result exceeding the byte budget:** return a distinct
  resource-limit error asking for a narrower lookup, not a silently incomplete list of matching chats. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Ambiguous ID with too many matching chats or a result exceeding the byte budget"]

- **Unsafe or unverifiable storage protection:** fail closed before opening for writes; an unsupported filesystem's
  permission behavior is not a reason to continue with weaker protection. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Unsafe or unverifiable storage protection"]

- **Cancellation near commit:** report success only for a confirmed commit; if the outcome cannot be established,
  return an explicit indeterminate outcome, not a claim of rollback. A retry uses the existing idempotent identity. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Cancellation near commit"]

- A network disconnect preserves saved login state, clears connected and generates no QR. Ordinary upstream reconnect is temporarily available only when same-active-generation safety is demonstrated; otherwise it is unavailable. It must stop on shutdown and cannot bypass terminal logout or the 405 budget. [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> "A network disconnect preserves saved login state, clears connected and"]

- A saved device identifier is not proof that WhatsApp still accepts the device. Live connection failure must not be presented as a successful login verification. [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> "A saved device identifier is not proof that WhatsApp still accepts the"]

- Expiry, successful connection, logout and cancellation racing each other must leave one consistent status snapshot and no stale pairing material. [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> "Expiry, successful connection, logout and cancellation racing each other"]

- A code's expiry is distinct from the pairing attempt's final timeout. An expired code is cleared immediately; the same approved attempt may supply a replacement. An unresolved terminal timeout requires explicit offline recovery and a separate pairing approval; Enter or end-of-input cannot authorize either. [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> "A code's expiry is distinct from the pairing attempt's final timeout. An"]

- Cancelling during the five-second retry wait or version discovery prevents subsequent connection attempts. [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> "Cancelling during the five-second retry wait or version discovery prevents"]

- Version discovery can stall, redirect unexpectedly, return oversized content, omit its revision, or return an invalid revision. These cases preserve the prior usable version and do not consume unlimited resources. [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> "Version discovery can stall, redirect unexpectedly, return oversized"]

- An empty or malformed QR event cannot be rendered or retained as a valid QR. Untrusted diagnostic text containing terminal controls cannot execute terminal actions. [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> "An empty or malformed QR event cannot be rendered or retained as a valid"]

- Storage failure during pairing is reported as failure, never as a durable successful pairing. Recovery must not delete conversation tables or other application data. [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> "Storage failure during pairing is reported as failure, never as a durable"]

- A shutdown timeout is not permission to close the database or release the lock while a device write remains active. [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> "A shutdown timeout is not permission to close the database or release the"]

- WhatsApp sends the restart request while pairing work is still finishing. The fresh connection waits for the check only up to the check deadline; it never starts while work from the pairing generation may still run. [Source: .specify/archive/004-shutdown-drain/spec.md -> "WhatsApp sends the restart request while pairing work is still finishing"]
- A logout arrives during the observation. It is latched as today; the marker clears only after durable cleanup. [Source: .specify/archive/004-shutdown-drain/spec.md -> "A logout arrives during the observation"]
- A pairing attempt times out, is cancelled, or gets a pairing success without a confirmed follow-up connection, and the stop then passes the check. The marker stays unsafe and offline recovery is required, because the phone may list a half-linked device. [Source: .specify/archive/004-shutdown-drain/spec.md -> "A pairing attempt times out, is cancelled, or gets a pairing success without a confirmed follow-up connection, and the stop then passes the check"]
- The disk is low when the marker would be cleared. The existing low-disk admission refuses the write; the marker stays unsafe and the operator is told to free space and run offline recovery. [Source: .specify/archive/004-shutdown-drain/spec.md -> "The disk is low when the marker would be cleared"]
- The observation of running work is larger than its bounded buffer. The result counts as incomplete and fails. [Source: .specify/archive/004-shutdown-drain/spec.md -> "The observation of running work is larger than its bounded buffer"]
- A library handler is writing to storage when the stop begins. It is not cut off by a cancelled context before the network and storage blocks are in place. If a guarded call still fails because of a context error, a stale lease or a refusal, the check fails. [Source: .specify/archive/004-shutdown-drain/spec.md -> "A library handler is writing to storage when the stop begins"]
- A stop signal arrives during the post-pair handover (while the pairing generation retires, between its retirement and the fresh generation's creation, or between creation and connect). No connection begins after the stop began, and every generation that exists is retired and checked before storage closes and the lock is released. The marker stays unsafe, because the pairing is unresolved. [Source: .specify/archive/004-shutdown-drain/spec.md -> "A stop signal arrives during the post-pair handover (while the pairing generation retires, between its retirement and the fresh generation's creation, or between creation and connect)"]
- A stop signal arrives in the instant between the library finishing its connect and the owner recording that fact, or between the owner's stop check and the connect call. Both interleavings fail closed and are counted or prevent the connect; they never clear the marker through a false pass. [Source: .specify/archive/004-shutdown-drain/spec.md -> "A stop signal arrives in the instant between the library finishing its connect and the owner recording that fact, or between the owner's stop check and the connect call"]
- The connect watchdog fires after the connection is established but before the connected event. It disconnects; it does not cancel the contexts the library's handlers run with. [Source: .specify/archive/004-shutdown-drain/spec.md -> "The connect watchdog fires after the connection is established but before the connected event"]
- The server closes the connection after the post-pair restart request. The resulting `Disconnected` after retirement is expected and does not fail the check. If the close arrives in the same instant as the request, the pinned client library can drop the request; the link then ends as incomplete and safe (known limitation on the feature 004 pin). [Source: .specify/archive/004-shutdown-drain/spec.md -> "The server closes the connection after the post-pair restart request"]
- A third-party library starts work on the client library's behalf. Only work named exactly in an allow-list may be ignored; anything else fails the check. [Source: .specify/archive/004-shutdown-drain/spec.md -> "A third-party library starts work on the client library's behalf"]
- The program's own event handlers or future tool code still hold the retired client. Their calls go through the same generation block and are refused and counted, so they cannot start new library work unseen. [Source: .specify/archive/004-shutdown-drain/spec.md -> "The program's own event handlers or future tool code still hold the retired client"]
- A dependency version change. The allow-list and the check must be revalidated against the new version before release. [Source: .specify/archive/004-shutdown-drain/spec.md -> "A dependency version change"]
- `stdio` cannot restart itself while a client holds its standard input and output, so the check runs inside the process. A process exit alone is not proof. [Source: .specify/archive/004-shutdown-drain/spec.md -> "`stdio` cannot restart itself while a client holds its standard input and output, so the check runs inside the process"]

- History and live deliveries can overlap, arrive out of order or contain identical IDs in different chats; identity is the pair of chat and message ID. [Source: .specify/archive/005-message-ingest/spec.md -> "History and live deliveries can overlap, arrive out of order or contain identical IDs in different chats; identity is the pair of chat and message ID."]
- Invalid identifiers, absent timestamps, malformed fields and unsupported message types do not create invalid message records; independent valid records remain eligible for processing. [Source: .specify/archive/005-message-ingest/spec.md -> "Invalid identifiers, absent timestamps, malformed fields and unsupported message types do not create invalid message records; independent valid records remain eligible for processing."]
- A group-history participant, an own message, and a direct-message sender must each be attributed to the actual sender when supplied; unresolved identity is not invented. [Source: .specify/archive/005-message-ingest/spec.md -> "A group-history participant, an own message, and a direct-message sender must each be attributed to the actual sender when supplied; unresolved identity is not invented."]
- Mapping lookup timeout or failure retains the original identity; a failure is not mistaken for proof that a mapping does not exist. [Source: .specify/archive/005-message-ingest/spec.md -> "Mapping lookup timeout or failure retains the original identity; a failure is not mistaken for proof that a mapping does not exist."]
- A LID whose mapping later points to a different phone number keeps the data already merged under the earlier phone; later messages follow the new mapping and the change is logged. [Source: .specify/archive/005-message-ingest/spec.md -> "A LID whose mapping later points to a different phone number keeps the data already merged under the earlier phone; later messages follow the new mapping and the change is logged."]
- History that arrives while `pair` is stopping, or after it stops, is reported as incomplete and is not recovered. [Source: .specify/archive/005-message-ingest/spec.md -> "History that arrives while `pair` is stopping, or after it stops, is reported as incomplete and is not recovered."]
- An "unavailable" placeholder is counted and reported; its message is not stored and is not requested from the phone. [Source: .specify/archive/005-message-ingest/spec.md -> "placeholder is counted and reported"]
- An individual store operation can fail due to cancellation, limits or low disk; stop ingest and the connected command safely and report the incomplete record, while retaining committed earlier batches. The operator fixes the problem and restarts under existing session-safety rules. [Source: .specify/archive/005-message-ingest/spec.md -> "An individual store operation can fail due to cancellation, limits or low disk; stop ingest and the connected command safely and report the incomplete record, while retaining committed earlier batches"]
- Receive overload closes the affected connection and is reported with a warning that local history may have a gap; retry or replay is not guaranteed. [Source: .specify/archive/005-message-ingest/spec.md -> "Receive overload closes the affected connection and is reported with a warning that local history may have a gap; retry or replay is not guaranteed."]
- Shutdown during ingest cannot allow writes after retirement or falsely establish a quiet stop; pending work is accounted for under feature 004's existing rules. [Source: .specify/archive/005-message-ingest/spec.md -> "Shutdown during ingest cannot allow writes after retirement or falsely establish a quiet stop; pending work is accounted for under feature 004's existing rules."]

- Missing required arguments, wrong types, unknown fields, empty required strings, unsupported JID domains and invalid sort values fail clearly, without account or conversation mutations. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "Missing required arguments"]
- Omission takes a default; `limit: 0` is invalid and explicit `include_last_message: false` remains false. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "Omitted defaults differ from explicitly invalid zero values"]
- Accept limits 1 through 200 and zero-based nonnegative integer pages; reject invalid limits, overflow and offsets over 100,000 before querying. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "Limits of 1 and 200 succeed"]
- Literal query punctuation, Unicode or terminal controls must not corrupt matching, inject a query or execute commands or terminal controls in rendered output. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "A query containing"]
- Missing activity or latest detail remains absent or null as applicable; never invent dates or messages. Empty lists are arrays, not null. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "Missing last activity and missing latest message"]
- Preserve distinct mapped phone and LID chats before merging and return each stored chat only once. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "Mapped phone and LID chat rows may coexist"]
- Tied latest timestamps select a deterministic real message; captions remain separate from message text. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "Tied latest-message timestamps"]
- Cancellation, EOF, slow readers and request floods must not leave unbounded work or release the lock before account and store work quiesce. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "Cancellation, end-of-input, slow output consumers and request floods"]
- Deferred login/QR calls fail as unknown tools without initiating pairing or revealing QR material. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "Calls to deferred login-status or pairing-QR tools"]
- Store busy, limit and closed errors are failures, not empty success; a terminal device-storage fault ends stdio rather than leaving it waiting. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "Store busy, resource-limit and closed errors"]

## Requirements

### Functional Requirements

**Command line (A3)**

- **FR-001**: The binary MUST be named `whatsapp-mcp` and offer exactly the commands `stdio`, `serve`, `pair`, [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-001]
  `status` and `version`, each with a one-line description in the help output.
- **FR-002**: `version` MUST print to stdout the version, source commit, build date, toolchain version and target [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-002]
  OS/architecture, and exit 0. Values not injected at build time MUST show as `dev` (version) or `unknown`.
  `version` MUST NOT read configuration, the data directory or the lock.
- **FR-003**: In this feature, `stdio` and `serve` MUST load and validate the configuration, prepare the data [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-003]
  directory, take the lock, log that the command's functionality is not implemented yet, and then run until a
  shutdown is requested. `stdio` MUST also treat the end of its stdin as a shutdown request.
- **FR-004** (historical binary skeleton, before the pairing and status contracts): `pair` validated configuration,
  prepared the directory and took the lock; `status` validated configuration and prepared the directory without
  taking the lock. Both reported that the command was not implemented and returned the general error code.
  [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-004]
- **FR-005**: The binary MUST use these exit codes: 0 for success and graceful shutdown; 1 for a general error, [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-005]
  including a held lock and a command not implemented yet; 2 for a usage error (unknown command or flag, missing
  command) and for invalid configuration.
- **FR-006**: Help MUST be available with `--help` / `-h` for the binary and for each command, listing that [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-006]
  command's flags with their environment variable and default.

**Configuration (C1, C4, D8, D10)**

- **FR-007**: Every setting MUST be resolved from these sources, highest first: command-line flag, environment [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-007]
  variable, `config.toml` in the data directory, built-in default.
- **FR-008**: Environment variable names MUST be `WHATSAPP_MCP_` followed by the setting name in upper case. No [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-008]
  other names, including the old names of §3.1 and §4.1, are read, and no alias or deprecation warning exists.
- **FR-009**: An environment variable set to the empty string MUST be treated as unset. [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-009]
- **FR-010**: Settings defined by this feature: [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-010]

  | Setting | Flag | Environment variable | `config.toml` key | Default | Valid values |
  | --- | --- | --- | --- | --- | --- |
  | Data directory | `--data-dir` | `WHATSAPP_MCP_DATA_DIR` | not allowed | per-user config dir + `/whatsapp-mcp` | absolute path |
  | Log level | `--log-level` | `WHATSAPP_MCP_LOG_LEVEL` | `log_level` | `info` | `debug`, `info`, `warn` (or `warning`), `error`; case-insensitive |

  Later features add their settings to the same mechanism.
- **FR-011**: The configuration file MUST be `config.toml` (TOML only) in the data directory. A missing file is [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-011]
  not an error; an unreadable or malformed file, an unknown key, a value of the wrong type, and the `data_dir` key
  MUST be errors.
- **FR-012**: All settings MUST be validated before the command does any other work. Every error MUST name the [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-012]
  setting, the source it came from (flag name, variable name, or file and line), the rejected value, and what is
  accepted. When several settings are invalid, all of them SHOULD be reported in one run.
- **FR-013**: An unknown environment variable with the `WHATSAPP_MCP_` prefix MUST produce a warning that names it [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-013]
  and MUST NOT stop startup.
- **FR-014**: At startup, the effective configuration and the source of each value MUST be logged at `debug` [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-014]
  level. Settings marked secret by later features MUST never be logged.

**Data directory (C2)**

- **FR-015**: The data directory MUST default to `whatsapp-mcp` inside the operating system's per-user [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-015]
  configuration directory, and MUST NOT depend on the working directory.
- **FR-016**: An override MUST be an absolute path; a relative path is a configuration error. [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-016]
- **FR-017**: When the data directory does not exist it MUST be created, including missing parents, accessible [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-017]
  only by the current user where the OS supports that. A path that exists but is not a directory, or cannot be
  created, MUST fail with a message naming it.
- **FR-018**: When no per-user configuration directory is available and no override is given, startup MUST fail [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-018]
  with a message suggesting `--data-dir` or `WHATSAPP_MCP_DATA_DIR`.

**Logging (M10)**

- **FR-019**: The process MUST have one structured logger, set up once from the effective log level and used by [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-019]
  all code. Later features bridge their libraries' logs into it.
- **FR-020**: All logs MUST go to stderr in every command. In `stdio` mode nothing other than MCP protocol traffic [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-020]
  may ever be written to stdout. Stdout is otherwise used only for a command's own output (`version` now,
  `status` later).
  WhatsApp client and device-store diagnostics MUST use the existing unified, level-controlled logger.
  Human pairing output in stdio mode MUST stay off stdout; no feature transport behavior may consume or
  contaminate protocol stdout. [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> FR-015]
- **FR-021**: Each log line MUST carry a timestamp, a level, a message and key-value attributes, in a [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-021]
  human-readable text form.
- **FR-022**: Configuration and logging errors that occur before the logger exists MUST still be reported on [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-022]
  stderr.

**Single-instance lock (A2, D14)**

- **FR-023**: `stdio`, `serve` and `pair` MUST take an exclusive OS-level lock on `<data dir>/whatsapp-mcp.lock` [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-023]
  before doing any work that touches the data directory beyond reading `config.toml`, and hold it until they
  exit.
- **FR-024**: The holder MUST write its PID into the lock file once it holds the lock. [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-024]
- **FR-025**: When the lock is held by another process, the command MUST NOT wait; it MUST fail at once with a [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-025]
  message that names the holder's PID (or says it is unknown) and the data directory.
- **FR-026**: The lock MUST be released by the OS when the holder exits for any reason, including a crash or a [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-026]
  forced kill, so no stale lock can block a later start. The lock MUST work on all five targets without a C
  toolchain.

**Shutdown (M10)**

- **FR-027**: Long-running commands MUST shut down gracefully on SIGINT and SIGTERM on Unix, and on Ctrl+C (and [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-027]
  Ctrl+Break) in a Windows console. Shutdown MUST drain the command's work before releasing the lock.
  Exit 0 on successful orderly shutdown; use the applicable failure exit rules otherwise (FR-029, FR-096, FR-151).
- **FR-028**: A second stop signal during shutdown MUST end the process at once with a non-zero exit code. [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-028]
- **FR-029**: If orderly shutdown is still incomplete at 10 seconds, the process MUST report the pending step
  and latch an unsuccessful exit. The deadline MUST NOT force process exit or authorize closing storage or
  releasing the lock before active work drains. Continue ordered cleanup until quiescent, then close storage,
  release the lock and exit nonzero. The explicitly unclean second-signal exit in FR-028 is separate.
  [Source: .specify/archive/003-whatsapp-pairing-status/contracts/connection-owner.md -> "Stop and Failure Contract"]

**Layout (A4)**

- **FR-030**: The code MUST follow the layout of A4 and the constitution: the entry point in `cmd/whatsapp-mcp` [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-030]
  and the configuration in `internal/config`, in the single module at the repository root.

**CI (Q2)**

- **FR-031**: A CI workflow MUST run on every push and pull request. It MUST fail when any Go file is not [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-031]
  `gofmt`-formatted (naming the files), when `go vet` reports a problem, or when `go test ./...` fails.
- **FR-032**: CI MUST build the binary for windows/amd64, linux/amd64, linux/arm64, darwin/amd64 and darwin/arm64 [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-032]
  with `CGO_ENABLED=0`, reporting each target separately.
- **FR-033**: CI MUST run the tests on Linux, Windows and macOS for every pull request and every push to `main`, [Source: .specify/archive/001-binary-skeleton/spec.md -> FR-033]
  because the lock and signal handling differ per operating system. Pushes to other branches MAY run the tests
  on Linux only, for quick feedback.

#### Database file (S1, D2, D16)

- **FR-034**: All persistent state MUST live in one database file, `<data dir>/whatsapp.db`, holding both the [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-001]
  whatsmeow device store and the message tables. No other database file is created and PostgreSQL is not supported.
  Connection-owning commands MUST use the single device session after taking the exclusive lock and validating
  storage, and MUST NOT import an earlier deployment. [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> FR-001]
- **FR-035**: The database MUST be opened with write-ahead logging, foreign-key enforcement, a busy timeout of at [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-002]
  least 5 seconds, and write transactions that take the write lock at their start, so that concurrent writers wait
  instead of failing.
- **FR-036**: The database driver MUST be pure Go and build for all five targets with CGO disabled. [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-003]
- **FR-037**: Creation and every subsequent open MUST verify private ownership/access for the data directory, [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-004]
  database, WAL/SHM, rollback journals and any sensitive SQLite temporary files. On Unix the intended process
  identity owns directories with mode `0700` and files with mode `0600`, with no additional ACL grants to unrelated
  identities; on macOS, where an extended ACL can grant access that the mode bits do not show, any extended ACL
  entry on these artifacts is refused. On Windows a verified DACL MUST restrict access to the intended user/service identity and explicitly
  documented required system/administrative principals; inherited broad grants MUST NOT expose the data. Go mode
  bits alone are not Windows access control. New artifacts MUST be protected from first creation; unsafe existing
  artifacts or unverifiable protection MUST fail startup without silent permission changes or deletion.
- **FR-038**: `stdio`, `serve` and `pair` MUST open the database after taking the data-directory lock and validating [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-005]
  FR-037/FR-073, and MUST close it during graceful shutdown. A database that cannot be opened MUST fail
  startup with a message naming the file and a safe reason under FR-081. No store, ingest or WhatsApp connection
  becomes available until opening and both schema upgrades succeed.
- **FR-039**: Files of the previous bridge (`store/*.db`) and its PostgreSQL database MUST NOT be read. [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-006]

#### Migrations (S2)

- **FR-040**: The message schema MUST be defined by numbered migrations embedded in the binary, applied in order at [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-007]
  open, and recorded with a version number kept separate from the device store's own version.
- **FR-041**: Migration 1 MUST create the complete message schema in an empty database. There is no older schema to [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-008]
  detect or adopt.
- **FR-042**: Each migration MUST be applied atomically together with its version record: on failure, the schema, [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-009]
  the data and the recorded version are as before that migration, and opening fails naming the migration. This is
  logical rollback of that step, not a promise that SQLite's main file or journal bytes never changed. Previously
  committed steps remain committed as specified in FR-074.
- **FR-043**: Before enabling write-affecting pragmas, recovering/checkpointing WAL or upgrading either schema, [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-010]
  existing storage MUST undergo read-only compatibility preflight for both schema versions, including committed
  state in an existing WAL. Unsupported newer versions, invalid/unrecognised version metadata, or corruption found
  by the preflight (a file that is not SQLite, a damaged header, unreadable schema or version tables) MUST stop
  opening without modifying the main file or existing sidecars, or creating/deleting sidecars. A full integrity
  check is not run on every start. Corruption elsewhere in the file fails the operation that reaches it, with a
  safe `corrupt` error, and is handled through the restore procedure (FR-080). Errors MUST
  identify the schema and supported/observed versions when readable, without sensitive contents. A genuinely
  uninitialised schema is version zero; missing version metadata for already-present tables is not a fresh schema.
  A version table counts as valid only with exactly one row, for the device schema as for the message schema,
  with one exception: an empty `whatsmeow_version` and no other `whatsmeow_*` table is a device schema that was
  never initialised (a first start interrupted between whatsmeow creating its version table and its first step),
  and counts as device version 0, so the next start resumes it.
  Two file states MUST also be refused before SQLite opens anything, because SQLite would otherwise delete a
  sidecar: a main file that is missing or empty while `-wal`, `-shm` or `-journal` exists, and any `-journal` next
  to the database, which only a crash during a journal-mode change or another tool leaves behind. The refusal
  names the files and points to the "Recovering from a refused start" steps of the operator guide (FR-080); nothing
  is deleted. A `-wal` that SQLite itself would ignore (an unknown header, an invalid page size or a header checksum
  that does not match) holds no commit and is ignored by the preflight too.
- **FR-044**: The device store's schema MUST be created or upgraded by whatsmeow's own mechanism when the database is [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-011]
  opened, only after both compatibility checks pass and before message migrations (FR-074). Its version support
  and failure guarantees MUST be verified for the pinned dependency rather than inferred from message migrations.

#### Chats and messages (S3, S4, D18, §3.2)

- **FR-045**: A chat MUST record its full JID, its name, the quality of that name (saved, real, fallback, or none [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-012]
  while the chat has no name yet), the time that
  name was seen, and its last activity time. Whether it is a group or a direct chat follows from the JID.
- **FR-046**: A message MUST record its ID, chat JID, full sender JID (without the device part), normalised sender [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-013]
  phone number (empty when the sender is an unresolved LID), time, whether it is from me, text, caption, media type,
  and the media metadata needed to download it later: URL, direct path, media key, file hash, encrypted file hash,
  file length, MIME type and original file name.
- **FR-047**: The store MUST derive the normalised sender phone number from the sender JID itself (digits only, for [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-014]
  phone-number JIDs), so the two can never disagree.
- **FR-048**: A message MUST be identified by `(chat_jid, id)`. Storing a message whose `(chat_jid, id)` exists MUST [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-015]
  replace its stored fields with the new copy's, except that an empty media metadata field (URL, direct path, media
  key, file hash, encrypted file hash, file length, MIME type, original file name) MUST NOT overwrite a stored
   non-empty one; storing never creates a second copy.
  Live and history duplicates MUST follow feature 002's composite identity, last-written replacement and preservation of non-empty media metadata; IDs shared by different chats MUST remain distinct. [Source: .specify/archive/005-message-ingest/spec.md -> FR-004]
- **FR-049**: Storing a message MUST create its chat if it does not exist and move the chat's last activity time [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-016]
   forward to the message's time if that is later, in the same atomic step.
  Chat creation and last activity MUST follow the existing atomic store rules; processing older messages MUST NOT move last activity backward (S4). [Source: .specify/archive/005-message-ingest/spec.md -> FR-005]
- **FR-050**: The store MUST offer a chat update (name, name quality, the time the name was seen, last activity [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-017]
  time) for chats known without a
  message, such as history-sync conversations. The last activity time MUST only ever move forward, and an absent time
  MUST NOT clear it.
- **FR-051**: A chat's name MUST be replaced only by a non-empty name that ranks higher by, in order: quality (saved [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-018]
  above real, real above fallback), then the time the name was seen (a later or equal time ranks at least as high), then byte-wise
  comparison of the names (greater wins). The outcome MUST NOT depend on the order in which names arrive. A name
  offered without a time, or with a time outside the approved timestamp range, is rejected.
- **FR-052**: A batch of messages within FR-076 count/byte limits MUST be storable in one transaction. Invalid or [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-019]
  oversized batches MUST be rejected as a whole without storing a prefix; callers split larger conversations.
- **FR-053**: The schema MUST support fast lookup by chat and time, by sender phone number, and by time. [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-020]

#### Single-message lookup (S6)

- **FR-054**: Looking up a message by `(chat_jid, id)` MUST return it or a distinct "not found" error that names the [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-021]
  chat and the ID.
- **FR-055**: Looking up a message by ID alone MUST return it when exactly one chat holds that ID, "not found" when [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-022]
  none does, and a distinct "ambiguous ID" error listing every matching chat JID when more than one does.
- **FR-056**: The store MUST return a message's media metadata by `(chat_jid, id)`, or a distinct "not a media [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-023]
  message" error.

#### Queries for the tools (§4.4, M5, M6)

- **FR-057**: Every message returned by a query MUST include ID, chat JID, chat name, sender JID, sender phone number, [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-024]
  sender name, time, from-me flag, text, caption and media type. The sender name of a message not from me is the
  contact's full, first, push or business name from the device store (in that order, so the address book comes
  first), looked up under the sender JID and then under its mapped phone JID or LID, else the saved or real name of
  the direct chat with the sender, else empty.
- **FR-058**: Every list query MUST take a page size and an offset, MUST return results in a deterministic order with [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-025]
  ties broken by a unique key, and MUST reject a non-positive page size, a negative offset, overflow or a request
  above FR-076 bounds. A direct store caller MUST NOT bypass those bounds.
  MCP list tools MUST accept limits 1–200 and zero-based integer pages; reject negative pages, nonintegers, overflow and offsets above 100,000. Empty results are successful empty lists. Do not truncate a successful page to meet a byte limit. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> FR-009]
- **FR-059**: List chats: filter by a text contained in the name or the JID; sort by last activity (newest first) or [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-026]
  by name (ascending); optionally include each chat's latest message by time.
  `list_chats` accepts optional `query`, `limit` (30), `page` (0), `include_last_message` (true) and `sort_by` (`last_active` or `name`). Match the stored chat name or JID by case-insensitive literal substring. Sort missing activity or unnamed chats last in their respective orders; break ties by ascending JID. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> FR-004]
- **FR-060**: Get chat: by JID, with its latest message; "not found" if absent. [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-027]
- **FR-061**: Search contacts: match the text against full name, push name, first name, business name and phone [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-028]
  number of the device store's contacts, excluding groups. A contact known by LID is reported with its phone number
  where the device store maps it; a person known under both a LID and a phone number is reported once, matched if
  either entry matches. Each result has a phone number (possibly empty), a name and a JID. For a person known under
  both, the result carries the phone-number JID and phone number, and the phone-number entry's first non-empty name
  in the order full, first, push, business; if that entry has none, the LID entry's name in the same order. A LID
  without a mapped phone number is reported with its LID JID and an empty phone number.
  MCP `search_contacts` requires a nonempty `query`, with literal case-insensitive substring matching and stable pagination for unchanged data (`limit` 50, `page` 0 by default). The public result has `jid`, `name` and a known `phone_number` or null; it does not change the store representation. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> FR-006] [Source: .specify/archive/006-mcp-stdio-tools/data-model.md -> "Contact"]
- **FR-062**: Direct chat by contact: the contact is resolved by FR-068. The result is the direct chat whose phone [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-029]
  number equals the contact's number exactly, else the contact's `@lid` chat if it still exists, or "not found".
  `get_direct_chat_by_contact` requires `sender_phone_number` (formatted phone number, phone JID or LID). Apply the same exact phone-first, mapped-LID-second precedence for equivalent inputs, even if only the LID row exists. Unmapped LIDs match their own exact chat. Never match a partial phone number or group; absence is a not-found tool error. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> FR-007]
- **FR-063**: Contact chats: the contact is resolved by FR-068. The result is the contact's direct chats (the [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-030]
  phone-number chat and the `@lid` chat, while both exist before a merge) plus every group in which the contact
  sent a message under either identity, each once, newest activity first, each with its real latest message.
  MCP `get_contact_chats` requires `jid` (accepting the same three contact forms), defaults to `limit` 20 and `page` 0, and includes **any** stored chat with a message from the person, not just groups, in addition to the person's exact direct chats. Return each actual chat once, newest activity first, ascending JID for ties, with the latest message regardless of sender. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> FR-008] [Source: .specify/archive/006-mcp-stdio-tools/contracts/store-and-lifecycle.md -> "Contact membership"]
- **FR-064**: List messages: filter by strictly-after time, strictly-before time, chat JID, sender and search text, [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-031]
  all optional and combined; newest first. The sender is a contact resolved by FR-068 and matches messages sent
  under either identity. The search text matches the text or the caption.
- **FR-065**: Message context: given a message, up to N earlier and M later messages from the same chat, both oldest [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-032]
  first. The same expansion MUST be available for every hit of a message listing, subject to per-side, aggregate
  row and result-byte limits in FR-076; checked arithmetic MUST precede expansion/allocation.
- **FR-066**: Last interaction: the contact is resolved by FR-068. The result is the newest message in any of the [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-033]
  contact's direct chats as defined in FR-063 (from either side) or sent by the contact in any group under either
  identity, or none. For the account's own number or LID, it is the newest message of the account's own direct
  chat, or none: the messages the account sent to others are not interactions with itself. Ties at the same time
  are broken by chat JID, then message ID, as in every listing.
- **FR-067**: Text matching in every query MUST ignore case across scripts (not only ASCII), MUST compare the search [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-034]
  text and the stored text after normalising both to Unicode NFC, MUST NOT ignore accents or other diacritics, and
  MUST treat wildcard and escape characters in the caller's text literally. Stored text is returned as originally
  received; normalisation applies only to matching.
- **FR-068**: The store MUST provide one phone-number normalisation used by every query and by storing: it accepts [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-035]
  digits with an optional `+`, spaces, dashes, dots and parentheses, or a phone-number JID, and yields the digits.
  Every query that takes a contact MUST also accept a LID JID, and MUST resolve the contact to its identities: the
  phone JID and number, the LID, or both, using the device store's LID-to-phone map (`whatsmeow_lid_map`, which is shared by all devices in the file) in
  both directions. A
  contact with no mapping has the one identity it was given. What ingest stores as a sender, and which LIDs it
  merges, remain ingest's decision (004).

#### LID chat merge (P2, §3.5)

- **FR-069**: The store MUST offer an operation that merges a LID into a given phone number atomically. It rewrites [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-036]
  every message whose sender is the LID, in every chat, to the phone JID with the phone number filled in. If the
  `@lid` chat exists, it also creates or updates the phone-number chat with the better name under FR-051 and the
  later last activity time, moves the messages, keeps the phone-number chat's copy when both hold the same message
  ID, and deletes the LID chat. The LID chat's name is offered to the phone-number chat only if it is saved or
  real; its fallback name is dropped.
- **FR-070**: Merging MUST be idempotent: merging a LID with no chat and no messages left under it does nothing and [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-037]
  reports no error. Merging a JID into itself, a source that is not `@lid`, or a target that is not a phone JID
  MUST be rejected.

#### Layout and tests (A4, Q3)

- **FR-071**: The store MUST live in `internal/store` in the single module, behind operations that the ingest, MCP and [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-038]
  media features call; no other package issues database queries. The one exception is whatsmeow's device store,
  which queries the same file through the container the store exposes to the WhatsApp client (003).
- **FR-072**: Automated tests MUST cover, against a database in a temporary directory: the migrations (fresh, [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-039]
  incremental, failing and too-new), upserts, the last activity time never going back, name quality, the LID merge,
  every query in FR-054–FR-067, and opening a database that holds both schemas. Tests MUST also cover FR-073–FR-083:
  Unix permissions and Windows ACLs (new/existing files, sidecars and sensitive temporary files), separate-identity
  access, path substitutions, combined schema failures, every approved resource boundary, cancellation/contention,
  disk-low/full failures, parameterized/literal queries, validation fuzz cases, secret-free diagnostics at all log
  levels, and protected offline backup/restore. Fixtures MUST be synthetic and tests MUST NOT connect to WhatsApp.
  Cross-identity/platform checks needing special privileges MUST have a documented isolated test procedure and
  recorded evidence before acceptance; an unavailable CI capability MUST NOT be reported as a passed test.

#### Connection, pairing, and recovery

- **FR-084**: `pair` MUST support QR-only interactive linking, display each current QR in the terminal, report success only after confirmed connection and saved credentials, and exit successfully after quiescing the connection. During one owner-approved attempt, replacement codes MUST be displayed automatically while invalidating the previous code. An unresolved final timeout, cancellation or failure MUST exit unsuccessfully with a useful offline-recovery diagnostic; Enter MUST NOT start another attempt. Recovery and a subsequent pairing each require separate explicit approvals.
  [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> FR-002]

- **FR-085**: A saved session with a clean marker MAY be reused without QR, including when an orderly stop cleared the marker under FR-110 after a production Connect attempt. An unsafe marker blocks automatic reuse on restart until offline recovery. Connection failure alone MUST NOT erase or silently replace the saved session.
  [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> FR-003]

- **FR-086**: The running connection owner MUST maintain a coherent status snapshot with `connected`, `logged_in`, `pairing_required` and `wa_version`. Confirmed connection sets connected and logged-in true and clears QR material. Disconnect clears only connected; confirmed logout clears connected and logged-in and invalidates pairing material.
  [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> FR-004]

- **FR-087**: `pairing_required` MUST retain the reference meaning: true only when not logged in and an unexpired QR is available. An expired QR or logged-out session without a new pairing request MUST report false. A returned QR MUST represent the current pairing attempt only.
  [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> FR-005]

- **FR-088**: After any production Connect attempt, deny a new same-process pairing even after confirmed logout and durable cleanup unless safety is proven and reviewed under a later decision. FR-099 offline recovery and separate fresh-pair approval are required. Logout alone never authorizes pairing; preserve conversations and never reuse revoked credentials.
  [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> FR-006]

- **FR-089**: Concurrent pairing requests MUST share one active attempt, including while its current code is expired and awaiting replacement; they MUST NOT silently start another attempt. Requests during shutdown MUST be rejected. Events from cancelled, timed-out or superseded attempts MUST NOT mutate current pairing or login state. Expiry of an individual code invalidates that code, not its still-active attempt.
  [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> FR-007]

- **FR-090**: `status` MUST print the four status fields and exit without connecting, fetching a version, generating a QR, migrating storage or altering saved account state. Missing storage MUST be reported as an unpaired local snapshot without creating artifacts. Existing storage inspection MUST respect the exclusive owner lock and private-storage checks; corrupt, unsupported, unsafe or locked storage MUST cause a clear failure.
  [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> FR-008]

- **FR-091**: Standalone `status` MUST distinguish local saved-session presence from live authentication: connected is false, logged-in reflects saved device identity, pairing-required is false, and the output warns that credentials have not been verified online. Its version is the locally available bundled version, explicitly labelled as not refreshed. It MUST NOT query or imply live knowledge of a running daemon.
  [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> FR-009]

- **FR-092**: Connection startup MUST attempt to discover the current WhatsApp Web version before connecting and MUST refresh it every six hours while running. Successful refresh applies to subsequent connections only; failure retains the last usable version or the bundled fallback and emits a warning.
  [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> FR-010]

- **FR-093**: Outdated-client recovery retains its budget of at most three retries after initial rejection, each after a cancellable five-second delay and version refresh, only where FR-099 permits a demonstrated-safe same-active-generation retry. Otherwise it is temporarily unavailable and reports the limitation. Repeated events MUST NOT multiply the budget; only confirmed connection resets it. Exhaustion stops automatic outdated reconnection and reports the cause.
  [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> FR-011]

- **FR-094**: The connection boundary MUST support offline substitutes for lifecycle, pairing, status and events, and the send, upload, download, contact-lookup and group-info capabilities required by later features. This feature MUST NOT enable message sending, downloading, ingest or MCP tools merely to establish that boundary.
  [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> FR-012]

- **FR-095**: The `stdio` and `serve` shells start the embedded owner when the marker permits, use a clean saved session or remain unpaired, and remain running until cancellation. The approved startup budget is at most three retries, five cancellable seconds apart, only where FR-099 permits demonstrated-safe same-active-generation retries; otherwise remain disconnected with an actionable limitation diagnostic and saved identity intact. It MUST NOT bypass 405, logout or shutdown. Neither shell exposes an automatic pairing QR or a network listener.
  [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> FR-013]
  Neither shell exposed tools in feature 003. Since feature 006, `stdio` exposes the five local read tools in FR-140; `serve` still has no MCP endpoint.
  [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> FR-002]

- **FR-096**: Shutdown MUST stop version refresh, pairing, reconnect work, event handling and all client-originated device writes before closing shared storage, then release the lock. It MUST not report clean shutdown or release ownership while those writes can still occur.
  [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> FR-014]
  Shutdown keeps its existing guarantees: every admitted device write finishes before storage closes, and the lock is released only after that. The check MUST NOT lengthen the existing 10-second stop bound for writes. The whole orderly stop, including the check, runs inside that same bound: the observation deadline is capped at the time left, and a stop that decides after the bound fails the check with the marker unsafe, so that a late decision never clears the marker. A decision that passed inside the bound can still be followed by the marker write, the store close and the lock release, and the lifecycle's timer covers them: the command may then report `shutdown timed out` and exit 1 with a clean marker, which is safe because the check passed and the stop completed in order. A failed check is not a failed shutdown; the process exits after an orderly close with the marker unsafe. [Source: .specify/archive/004-shutdown-drain/spec.md -> FR-011]

- **FR-097**: Device operations MUST share the approved store admission, deadline, input/output and disk policy. Before changing app-state sync progress or a bulk contact snapshot, the owner MUST durably record the affected collection's repair need. An interrupted/failed sync MUST retain that need across restart. After an explicitly authorized connection, bounded re-synchronization MAY repair only the affected protocol metadata/contact snapshot; clear the indication only after complete verified success. Failure MUST remain visible and retryable without automatic unpairing, deleting chats/credentials/session keys, silently truncating data or claiming rollback of earlier committed operations.
  [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> FR-016]

- **FR-098**: Fresh QR registration MUST use the previous service's companion OS `Linux` and platform `CHROME`, including the matching Chrome QR client type. Its registration version and user-agent OS version/build MUST use one selected handshake version, as in the previous service. The adapter MUST preserve credentials and all unrelated payload properties, including unknown protobuf fields, keep shared library globals immutable, and reuse an existing saved identity without forcing re-pairing when FR-099 permits reuse. A failure to decode or encode companion properties MUST fail the handshake without sending an incorrectly identified registration, preserve saved credentials and record a secret-free diagnostic category for reporting outside the callback. Repeated failures may coalesce; forced termination or an indefinitely stalled logger cannot guarantee delivery. WhatsApp determines the displayed name; no exact phone label or retroactive relabelling is promised.
  [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> FR-017]

- **FR-099**: Before account-capable work, durably arm the unsafe-run marker; failed or ambiguous arming admits no account work. After a production Connect attempt, including failure or cancellation, clear at orderly shutdown only under FR-110. Without such an attempt, clear only after orderly drain and durable completion of any confirmed-revocation cleanup. Unsafe, unreadable, invalid or ambiguous state blocks automatic connection, pairing and QR after restart. The marker does not prove revocation: offline recovery requires explicit operator verification of the account/device, separately authorized phone-side unlink if needed, and separately authorized local removal of only the device identity if present before clearing; a later fresh pairing requires separate approval. Never silently reuse an uncertain identity, infer safe state from a missing device row, remove chats/messages, or treat an automatic restart or ordinary Enter as consent. Existing pre-marker databases are unknown; new databases may start clean only when creation is known under the exclusive lock. `status` remains offline and non-migrating, honestly reports local identity presence when inspectable, warns and exits unsuccessfully for unsafe or legacy-unknown state.

  Verification must identify the exact account and linked-device entry, or establish that the exact device is absent; a generic Linux/Chrome label, timestamp, guess, or absence of a local row is not sufficient. If entries cannot be distinguished, the operator may separately authorize and manually unlink every potentially matching entry, after a warning that unrelated devices may be disconnected, and verify their absence. If identification or absence remains unverified, recovery remains blocked, with no local removal or marker clear. No exact phone label or reliable identifying attribute is guaranteed. Offline recovery requires verified interactive input and output as specified in the CLI contract; unsupported or redirected terminals fail closed. This policy does not itself authorize phone-side action.

  Startup retries, 405 recovery, ordinary reconnect, other 515 reconnects and same-process re-pair remain denied with diagnostics under FR-113. No further Connect is permitted on or after retirement of an unproven upstream generation, including on a replacement client. The marker latch alone does not prove that a retry is safe.

  The sole post-pair reconnect is the owner-initiated `pair` handover under FR-111 and FR-112: after the pairing generation passes its quiet-stop check, make one attempt on a fresh client and generation under the single attempt slot and the 30-second watchdog. `stdio` and `serve` do not reconnect. If the required follow-up is unavailable, fails or stalls, report incomplete linking and required offline recovery, retain unsafe and exit unsuccessfully. PairSuccess alone is not a connected or usable link. The earlier same-client approval, switch and T077 review gate remain in the archived feature 003 evidence, not as current requirements.

  A logout or client deletion request recognized before its generation's callback and device admissions have drained is latched through retirement: clearing requires durable cleanup of that revocation, otherwise the marker stays unsafe. Low-disk admission, including its existing resume hysteresis, applies to cleanup and marker clearing without exemption; refusal leaves unsafe state and an actionable instruction to restore free space and perform offline recovery. Durability against power loss is conditional on the actual platform synchronization implementation and storage honoring it; SQLite WAL FULL alone does not establish macOS full hardware flush. Native evidence and limitations are required before making platform-specific claims.

  Identity removal, whether after confirmed revocation or separately authorized offline recovery, MUST retain pending FR-097 collection-repair needs across device-row cascades, restart and fresh pairing. Materialize those needs for the new saved identity before ordinary app-state progress and clear them only after successful fenced repair. Device-specific credentials/keys may be removed only as part of genuine confirmed revocation or separately authorized offline identity removal, never as a shortcut for app-state repair.
  [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> FR-018] [Source: .specify/archive/004-shutdown-drain/spec.md -> FR-003] [Source: .specify/archive/004-shutdown-drain/spec.md -> FR-004] [Source: .specify/archive/004-shutdown-drain/spec.md -> FR-005] [Source: .specify/archive/004-shutdown-drain/spec.md -> FR-006]

- **FR-108**: Stopping a generation MUST happen in this order: block that generation's network traffic (socket and HTTP), then block its storage access, then observe the client library's remaining work. Storage MUST NOT be blocked while the generation's network can still receive, because a refused write during decryption can make the library acknowledge and lose a message. [Source: .specify/archive/004-shutdown-drain/spec.md -> FR-001]

- **FR-109**: A generation passes the quiet-stop check only if, by the owner-approved deadline (see Assumptions), (a) none of the client library's work is running, except work named exactly in a reviewed allow-list, (b) no dial was refused on the generation's fenced network clients after the network block (this proves isolation of the retired client; it cannot see a library send after the library's own disconnect, which the observation covers). A write on a connection that the block already closed is refused with nothing sent and counted, but does not fail the check, because TLS and HTTP/2 teardown write close messages by design, (c) no guarded storage call of the generation failed after retirement began because of a refusal, a context error, a stale lease or another guard error; a read refused only because the storage block has retired is counted separately and does not fail the check, because it changes nothing and the network is already closed when that block retires, (d) no call from the program's own code to the session was refused after retirement began, (e) no event that the session would have forwarded to the owner was dropped after retirement, except `Disconnected` and message events covered by FR-138, and (f) the observation is complete. The check also fails when the whole stop has passed its bound (see FR-096). Every other outcome, including an error, a cut-off observation or the deadline, fails. [Source: .specify/archive/004-shutdown-drain/spec.md -> FR-002] [Source: .specify/archive/005-message-ingest/contracts/ingest.md -> "Revised event-count rule"]

- **FR-110**: After a production connection attempt, the marker MAY be cleared at orderly shutdown only when every generation of the run passed the check and all existing safe-clear prerequisites hold: storage drained, any latched logout or client deletion durably cleaned up, no unresolved pairing attempt, and low-disk admission granted. A pairing attempt is unresolved unless it ended in a confirmed connection with a durable identity: a QR timeout, a cancellation, or a pairing success without a confirmed follow-up connection all count as unresolved, because the phone may list a half-linked device that only the operator can check. Otherwise the marker MUST stay unsafe and the next start requires offline recovery, as today. [Source: .specify/archive/004-shutdown-drain/spec.md -> FR-003]

- **FR-111**: The post-pair reconnect MUST run on a fresh client and a new generation, built from the durably saved identity, and only after the pairing success has been handled and the pairing generation has passed the check. It keeps feature 003's rules: a restart request before the pairing success waits for it within the 30-second watchdog, and a pairing error, logout, stop or the watchdog refuses it. It is one owner-initiated attempt under the single attempt slot and the 30-second watchdog. The fresh client MUST log in with the saved identity (no new registration) and the currently selected WhatsApp Web version and Linux/Chrome identification, and a test MUST assert the selected version on this connection. A failure, stall, cancellation or failed check is final: report an incomplete link that needs offline recovery, keep the marker unsafe and exit unsuccessfully. [Source: .specify/archive/004-shutdown-drain/spec.md -> FR-004]

- **FR-112**: The code-level `PostPairReconnect` switch MUST be removed. The post-pair reconnect is part of `pair` only; `stdio` and `serve` do not pair and have no reconnect. [Source: .specify/archive/004-shutdown-drain/spec.md -> FR-005]

- **FR-113**: No other automatic or repeated connection is added. Startup retries, the 405 budget, ordinary reconnect, any other 515 reconnect and same-process re-pair stay denied with their existing diagnostics. [Source: .specify/archive/004-shutdown-drain/spec.md -> FR-006]

- **FR-114**: Every path from the program's own code to a client MUST go through that client's generation block (connect, QR channel, repairs, disconnect, store access). After retirement began, such calls are refused and counted toward the check, except the retirement sequence's own disconnect. The context the library's node handlers run with is not cancelled before the network and storage blocks are in place (FR-108), so that a handler cannot lose a write to a cancelled context unseen. This holds for the stop signal itself, which cancels the owner context before retirement starts: the library context does not derive from it, and the connect watchdog and the startup attempt's cleanup do not cancel it once the connection is established. Counting of failed guarded calls starts at the stop signal, at the start of retirement or at the first cancellation of the attempt context, whichever comes first. [Source: .specify/archive/004-shutdown-drain/spec.md -> FR-007]

- **FR-115**: The observation MUST have a bounded memory size and a bounded duration. Its raw contents MUST NOT be logged, stored, returned or included in errors at any level. Only the outcome, a failure category, counts and the duration may be reported. [Source: .specify/archive/004-shutdown-drain/spec.md -> FR-008]

- **FR-116**: Each check MUST report its outcome on stderr in operator wording: reusable, or offline recovery required with the category. The check's outcome replaces the blanket "upstream drain unproven" latch and its `upstream_drain_unproven` event; the `run_guard_recovery_required` event stays for every stop that leaves the marker unsafe. stdout in stdio mode stays protocol-only. [Source: .specify/archive/004-shutdown-drain/spec.md -> FR-009]

- **FR-117**: The allow-list MUST name exact functions, never a module or package prefix. It is tied to the pinned client library version; a version change MUST fail a test until the allow-list is revalidated. [Source: .specify/archive/004-shutdown-drain/spec.md -> FR-010]

- **FR-118**: Offline recovery, `status` and the marker's storage format stay as they are. A clean marker after a passed stop MUST look exactly like a clean marker after a run without a production connection. [Source: .specify/archive/004-shutdown-drain/spec.md -> FR-012]

- **FR-119**: The specification's temporary fail-closed limitation (§8.11, 2026-11-04) and the post-pair reconnect approval (§8.11, 2026-11-06) MUST be updated to the 2026-11-07 decision before implementation starts. Feature 003's spec, contracts and `verification.md` stay as the historical record; `OPERATIONS.md` and `AGENTS.md` MUST be updated to describe a reusable session after a passed check, and to say that offline recovery is still needed after a failed one. [Source: .specify/archive/004-shutdown-drain/spec.md -> FR-013]

#### Message ingest

- **FR-126**: The system MUST ingest supported live messages and history supplied by WhatsApp, including account-originated messages, without requiring a separate history request (P3, §3.4). [Source: .specify/archive/005-message-ingest/spec.md -> FR-001]

- **FR-127**: Each stored message MUST retain its chat identity, message ID, full sender JID without a device component, normalized phone number when known, timestamp, direction, text, separate caption, media type and available download metadata, including URL, direct path, key, hashes, length, MIME type and original filename (S3, P3, D18). [Source: .specify/archive/005-message-ingest/spec.md -> FR-002]

- **FR-128**: Text includes ordinary and extended text; supported media are image, video, audio and document. Image, video and document captions MUST be stored separately from text. A message with neither supported text nor recognized media MUST NOT be stored (§3.4, D18). [Source: .specify/archive/005-message-ingest/spec.md -> FR-003]

- **FR-129**: The system MUST resolve chat and sender LIDs to phone identities when the mapping is known; on no mapping or lookup failure it MUST retain the LID and MUST NOT invent a phone number. Mapping lookups MUST have a two-second maximum wait (§3.5, P2). [Source: .specify/archive/005-message-ingest/spec.md -> FR-006]

- **FR-130**: At startup, before live ingest can race the reconciliation, the system MUST reconcile mapped LID chats using feature 002's atomic, idempotent merge, including sender rewrites across chats, phone-copy precedence on duplicate IDs and its name-quality rules. It MUST also attempt that merge when a new mapping is detected during a running session. Unmapped or failed items remain intact and are reported; repeat startup retries them. Mappings the startup pass did not reach within its time budget are continued by the running session (P2). [Source: .specify/archive/005-message-ingest/spec.md -> FR-007]

- **FR-131**: The system MUST consult a stored name first, use available history names for groups, and cache group-information results rather than fetch them for each message. It MUST apply feature 002's saved, real and fallback name ranking, observation time and tie-breaking rules (M7, S4). [Source: .specify/archive/005-message-ingest/spec.md -> FR-008]

- **FR-132**: When no better name is available, group names fall back to `Group <id>`; direct names use the available saved contact name, then self-chosen contact name, sender name or JID user part, consistent with feature 002's quality ordering. Failed name lookup MUST NOT by itself prevent storing a valid message (§3.4). [Source: .specify/archive/005-message-ingest/spec.md -> FR-009]

- **FR-133**: History processing MUST submit batches within the existing store limits of 1,000 messages and 16 MiB of aggregate field bytes, and MUST NOT buffer unbounded application-owned work. On a storage failure affecting a live message or history batch, ingest MUST stop, report the failure and incomplete record, and stop the connected command safely. The operator fixes the problem and restarts under existing session-safety rules; previously committed messages remain intact and MUST NOT be represented as a completely successful sync. [Source: .specify/archive/005-message-ingest/spec.md -> FR-010]

- **FR-134**: A valid message's sender MUST reflect its supplied sender or history participant, or the account identity for an own message. An absent or invalid required sender, chat, ID or time MUST NOT be replaced with a fabricated value merely to make a record valid. [Source: .specify/archive/005-message-ingest/spec.md -> FR-011]

- **FR-135**: Stored live messages MUST be made available to the later event-delivery feature with their normalized identity, text, caption and media information; history MUST NOT be emitted as a live event. Webhook delivery itself is outside this feature. [Source: .specify/archive/005-message-ingest/spec.md -> FR-012]

- **FR-136**: Ingest MUST participate in the existing generation and shutdown safety rules: stop admitting work at retirement, account for admitted work, and preserve the network-block, storage-barrier and quiet-check ordering. An incomplete or uncertain stop MUST NOT clear the unsafe marker. [Source: .specify/archive/005-message-ingest/spec.md -> FR-013]

- **FR-137**: Automated synthetic-event tests MUST cover live and history, captions and media metadata, out-of-order and duplicate delivery, group and own senders, mapping failures, merge collisions, name caching, bounded batches, failure and shutdown interleavings. Real-account acceptance requires separate permission for each pairing, connection and receiving step; review the roadmap's shutdown follow-up before that test. [Source: .specify/archive/005-message-ingest/spec.md -> FR-014]

- **FR-138**: A message event dropped after the generation retire bit is counted separately as `DroppedMessages`, left unacknowledged and reported as a possible gap. It does not count as a dropped owner-relevant event for the quiet-stop check; all other parts of the owner event rule still apply. [Source: .specify/archive/005-message-ingest/contracts/ingest.md -> "Revised event-count rule"]

#### Local MCP over stdio

- **FR-139**: `whatsapp-mcp stdio` MUST serve an embedded MCP session under the existing exclusive owner, configuration, private store and connection lifecycle. A held lock is a clear failure; no implicit proxy, HTTP listener or second owner. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> FR-001]
- **FR-140**: The stdio read suite exposes exactly `list_chats`, `get_chat`, `search_contacts`, `get_direct_chat_by_contact` and `get_contact_chats` with their preserved purposes. Login-status and pairing QR are deferred; message, send and media tools are later work. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> FR-002]
- **FR-141**: Advertised schemas MUST use snake_case, declare required fields, real defaults, ranges and enums. Handlers independently default omitted values and validate supplied values; preserve explicit false and reject unknown fields with actionable invalid-input errors. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> FR-003]
- **FR-142**: `get_chat` requires `chat_jid` and defaults `include_last_message` to true. Fetch that exact stored chat, including groups and unresolved LIDs, or return a not-found tool error. Disabling latest detail MUST preserve last-activity metadata. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> FR-005]
- **FR-143**: Chat results carry `jid`, `name`, group identity, nullable last activity and optional latest detail. Latest detail carries composite `chat_jid`/`id`, sender identity and name, timestamp, own-message flag, text, caption and media type, with no media keys or credentials. Timestamps MUST use RFC 3339; the five tools take no timestamp filter. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> FR-010]
- **FR-144**: Successful tool results MUST include authoritative structured content and an equivalent, safely rendered text value without double encoding. No compact `format: "text"` mode. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> FR-011]
- **FR-145**: Invalid arguments, absent singular objects and bounded-operation failures MUST produce clear tool errors, not opaque internal errors or empty successes. Malformed protocol requests may use a protocol error. Identify the field or unavailable capability without disclosing raw queries, storage errors, credentials or unnecessary paths. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> FR-012]
- **FR-146**: Do not register `get_login_status`; keep offline CLI `status` unchanged. Live status is deferred to `docs/backlog/006-mcp-login-tools.md`. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> FR-013]
- **FR-147**: Do not register `get_pairing_qr`; terminal `pair` remains the pairing path. No stdio pairing opt-in, automatic pairing or tool-controlled window; recovery and reconnect gates remain unchanged. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> FR-014]
- **FR-148**: Chat/contact reads use local data and mappings, preserve stored-name precedence and never fetch WhatsApp contacts or group info. Serve those reads offline when the store and read policy permit; inherit existing startup account behavior. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> FR-015]
- **FR-149**: Owner launch authorizes local reads of all stored chats and contacts without per-chat restrictions or per-read approval. Neither launch, tool inputs nor returned content authorizes pairing, recovery or sending. Configurable restrictions are deferred for research; the owner decision is recorded in specification §8.11. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> FR-016]
- **FR-150**: Tool pressure MUST NOT exhaust shared store admission or stop ingest by flooding it. Bound actual input/output bytes, field sizes, concurrency, queued work and duration; propagate cancellation and reject overload explicitly. Planning must set concrete ceilings and a testable admission policy before implementation. No unbounded default or inherited receive exception. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> FR-017]
- **FR-151**: On EOF, disconnect or stop signal, stop admission and cancel and drain requests before releasing account, fence, store and lock resources in the existing order. Terminal device-store or ingest failure MUST enter normal unsuccessful shutdown, never remain waiting or claim quiet stop or clear the marker without proof. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> FR-018]
- **FR-152**: Through the official SDK in-memory transport, test all five tools for success and rejection using a seeded temporary store and fake owner, with no real account or account network. Prove the two deferred tools are absent and fail without account work. Also cover initialization, protocol-only stdout, lock refusal, cancellation, EOF and terminal faults at command level. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> FR-019]

### Key Entities

#### Setting

one configuration value, with its name, flag, environment variable, file key, default, validation
rule, and whether it is secret. [Source: .specify/archive/001-binary-skeleton/spec.md -> "Setting"]

[Source: .specify/archive/001-binary-skeleton/data-model.md -> "Setting (`internal/config`)"]

A descriptor in the settings registry. It is the single definition from which the flags, the help text, the
environment lookup, the file checks and the debug dump are generated (research R3).

| Field | Type | Meaning |
| --- | --- | --- |
| `Name` | string | Canonical snake_case name, for example `log_level`. Used in error messages and the debug dump. |
| `Flag` | string | Flag name without dashes, for example `log-level`. |
| `Env` | string | `WHATSAPP_MCP_` + upper-case `Name`. Derived, never written by hand. |
| `FileKey` | string | Key in `config.toml`. Empty means the setting is not allowed in the file. |
| `Default` | func(env) (string, error) | Default value as text. A function, so the data directory can call `os.UserConfigDir`. |
| `Usage` | string | One-line description for help. |
| `Secret` | bool | When true, the value is never logged or printed (none in this feature). |
| `ParseText` | func(string) (any, error) | Parses and validates a flag or environment value. |
| `ParseTOML` | func(any) (any, error) | Checks the TOML type, then validates as `ParseText` does. |
| `Apply` | func(*Config, any) | Stores the parsed value into the typed `Config`. |

**Settings in this feature:**

| Name | Flag | Env | File key | Default | Validation |
| --- | --- | --- | --- | --- | --- |
| `data_dir` | `data-dir` | `WHATSAPP_MCP_DATA_DIR` | (not allowed) | `os.UserConfigDir()` + `whatsapp-mcp` | absolute path (`filepath.IsAbs`), then `filepath.Clean` |
| `log_level` | `log-level` | `WHATSAPP_MCP_LOG_LEVEL` | `log_level` (TOML string) | `info` | case-insensitive `debug`, `info`, `warn`, `warning` (= `warn`), `error` → `slog.Level` |

**Registry rules:**

- Names are unique.
- `Env` equals `"WHATSAPP_MCP_" + strings.ToUpper(Name)` (FR-008).
- `FileKey` is either empty or equal to `Name`.

A unit test enforces all three.

#### Effective configuration

the resolved value of every setting, together with the source each value came from.
It is read-only after startup. [Source: .specify/archive/001-binary-skeleton/spec.md -> "Effective configuration"]

[Source: .specify/archive/001-binary-skeleton/data-model.md -> "Config (`internal/config`)"]

The effective configuration. It is built once by `config.Load` and is read-only afterwards.

| Field | Type | Notes |
| --- | --- | --- |
| `DataDir` | string | Absolute and cleaned. The directory may not exist yet; see "Data directory". |
| `LogLevel` | `slog.Level` | |
| `origins` | map[name]Origin | Read with `Origin(name)`, and used by `LogValues` for the debug dump. |

`Load` takes an `Inputs` value and returns `(*Config, []Warning, error)`:

| `Inputs` field | Type | Production value |
| --- | --- | --- |
| `Flags` | map[string]string | Only the flags that were set (`FlagSet.Visit`). |
| `Environ` | []string | `os.Environ()`. |
| `UserConfigDir` | func() (string, error) | `os.UserConfigDir`. |

- A `Warning` is an unknown `WHATSAPP_MCP_*` variable. The caller logs it once the logger exists.
- The returned error joins every `*SettingError` and `*FileError` found (FR-012).

**Validation errors:**

| Type | Fields | Raised for |
| --- | --- | --- |
| `SettingError` | `Setting`, `Origin`, `Value`, `Reason` (includes the accepted values) | An invalid value from any source. A relative data directory. |
| `FileError` | `Path`, `Line` (0 if unknown), `Column` (0 if unknown), `Key`, `Reason` | TOML syntax, an unreadable file, an unknown key, a wrong type, `data_dir` in the file. |

The message formats are in [configuration contract](../archive/001-binary-skeleton/contracts/configuration.md).

#### Data directory

the per-user directory that holds `config.toml`, the lock file and, in later features, the
database, `downloads/`, `outbox/` and the generated secrets. [Source: .specify/archive/001-binary-skeleton/spec.md -> "Data directory"]

[Source: .specify/archive/001-binary-skeleton/data-model.md -> "Data directory"]

| Path | Created by | Notes |
| --- | --- | --- |
| `<data dir>/` | `config.PrepareDataDir` after successful validation | Existing directories are not re-permissioned. A path that is not a directory is an error. See the [configuration contract](../archive/001-binary-skeleton/contracts/configuration.md#data-directory-and-file-privacy). |
| `<data dir>/config.toml` | the operator | Optional; never written by the binary. |
| `<data dir>/whatsapp-mcp.lock` | `lock.Acquire` | See "Lock file". |
| `whatsapp.db`, `downloads/`, `outbox/`, secrets | features 002–010 | Not created here. |

**Who touches the data directory, per command:**

| Command | Reads `config.toml` | Creates the directory | Takes the lock |
| --- | --- | --- | --- |
| `version` | no | no | no |
| `status` | yes | yes | no |
| `pair` | yes | yes | yes |
| `stdio`, `serve` | yes | yes | yes |

#### Lock file

`<data dir>/whatsapp-mcp.lock`. The OS lock on it marks ownership; its content is the holder's
PID, for messages only. [Source: .specify/archive/001-binary-skeleton/spec.md -> "Lock file"]

[Source: .specify/archive/001-binary-skeleton/data-model.md -> "Lock file (`internal/lock`)"]

`<data dir>/whatsapp-mcp.lock`. The OS lock on byte 0 means ownership. The content serves only the error
message.

| Byte range | Content |
| --- | --- |
| 0 | Reserved, with no meaning (NUL in a new file). It is never read: on Windows it is under a mandatory lock (research R6). |
| 1 … | The holder's PID in decimal ASCII, then `\n`. |

**Types:**

- `Lock`: `Path`, the `*flock.Flock`, and a second write handle. `Release()` truncates the file to 0 bytes,
  unlocks it and closes both handles. It is idempotent.
- `HeldError`: `Path`, `DataDir`, `PID int` (0 = unknown). Returned by `Acquire` when `TryLock` reports that
  another process holds the lock.

**States of the file:**

```text
absent ──Acquire──► locked (PID written) ──Release──► unlocked, 0 bytes
                        │
                        └──process dies──► unlocked by the OS, stale PID left (ignored)
unlocked (any content) ──Acquire──► locked (content replaced by the new PID)
```

#### Build information

version, commit, build date, toolchain version and target, fixed at build time. [Source: .specify/archive/001-binary-skeleton/spec.md -> "Build information"]

[Source: .specify/archive/001-binary-skeleton/data-model.md -> "Build information (`internal/buildinfo`)"]

| Field | Source | Fallback |
| --- | --- | --- |
| `Version` | `-ldflags -X …/buildinfo.Version=` | `dev` |
| `Commit` | `-ldflags -X …/buildinfo.Commit=` | `vcs.revision` (+ `-dirty`), then `unknown` |
| `Date` | `-ldflags -X …/buildinfo.Date=` (RFC 3339) | `vcs.time`, then `unknown` |
| `GoVersion` | `runtime.Version()` | — |
| `Target` | `runtime.GOOS + "/" + runtime.GOARCH` | — |

#### Origin

[Source: .specify/archive/001-binary-skeleton/data-model.md -> "Origin (`internal/config`)"]

Where a resolved value came from. It is used in error messages and the debug dump.

| Field | Type | Meaning |
| --- | --- | --- |
| `Source` | enum `Default`, `File`, `Env`, `Flag` | The layer that won. Precedence: `Flag` > `Env` > `File` > `Default`. |
| `Detail` | string | `--log-level`; `WHATSAPP_MCP_LOG_LEVEL`; `<path>/config.toml line 3`; or `default`. |

#### Run lifecycle

[Source: .specify/archive/001-binary-skeleton/data-model.md -> "Run lifecycle (`cmd/whatsapp-mcp`)"]

Long-running commands (`stdio`, `serve`) use this lifecycle. `pair` and offline `status` follow their own
command contracts; the skeleton's unimplemented-command exit is historical.

```text
starting ──config invalid──────────────► exit 2
   │  (signal handler installed first)
   ├──data dir unusable / lock held──────► exit 1
   ▼
running ──1st signal / stdin EOF (stdio)──► stopping
                                               │ stop steps run in reverse order
                                               ├──clean completion────────► exit 0
                                               ├──10 s while pending──────► report pending step, latch failure,
                                               │                             retain ownership through drain,
                                               │                             finish cleanup, then exit 1
                                               └──2nd signal──────────────► exit 1 at once
```

- **Stop step:** `{Name string; Stop func(context.Context) error}`.
- A step is registered as soon as its resource is taken. A signal during `starting` still cleans up acquired
  resources; it does not erase a failure or allow early unlock. See FR-029 and FR-151.
- A step that returns an error is logged at `ERROR`, the remaining steps still run, and the exit code is 1.

#### Database file

`<data dir>/whatsapp.db`, the single store for device keys and message history, with its
write-ahead journal files next to it. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Database file"]

[Source: .specify/archive/002-sqlite-storage/data-model.md -> "The file"]

`<data dir>/whatsapp.db`, plus `whatsapp.db-wal` and `whatsapp.db-shm` while open. It holds two independent schema
histories:

| History | Version record | Owner | Tables |
| --- | --- | --- | --- |
| Device store | `whatsmeow_version (version, compat)` | whatsmeow `sqlstore` (R3) | `whatsmeow_*` |
| Message store | `mcp_version (version)` | this feature's migrations (R5) | `mcp_version`, `chats`, `messages` |

The store **reads** these whatsmeow tables and never writes them: `whatsmeow_device` (the current device's JID),
`whatsmeow_contacts` and `whatsmeow_lid_map (lid TEXT PRIMARY KEY, pn TEXT UNIQUE NOT NULL)`. The LID map is not per device,
and it holds bare user parts: `lid` without `@lid`, `pn` without `@s.whatsapp.net`. The store adds the suffixes
when it compares them with stored JIDs.

All message-store tables are `STRICT`, so SQLite rejects a value of the wrong type instead of storing it.

#### Message schema version

the number of the last message-schema migration applied; separate from the device
store's version. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Message schema version"]

[Source: .specify/archive/002-sqlite-storage/data-model.md -> "`mcp_version`"]

| Column | Type | Rules |
| --- | --- | --- |
| `version` | INTEGER NOT NULL | exactly one row; the number of the last applied migration |

The preflight refuses zero rows, more than one row, a version above the latest bundled migration, or `chats`/
`messages` present without this table (R4).

#### Migration

a numbered, embedded schema change, applied once and in order. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Migration"]

#### Chat

The public MCP `Chat` represents a stored direct, group or unresolved-LID conversation with full `jid`, possibly empty `name`, `is_group`, and a UTC RFC 3339 `last_message_time` or null. `latest_message` is omitted if not requested and null if requested but absent. Contact-chat tools always request it. Distinct phone and LID rows remain distinct until merged; the singular direct tool prefers the phone row. This projection does not change persistence. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "Chat"] [Source: .specify/archive/006-mcp-stdio-tools/data-model.md -> "Chat"]

a conversation identified by its full JID (direct or group), with a name, the quality of that name (saved, real or fallback), the time
the name was seen, and the time of its newest stored message. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Chat"]

[Source: .specify/archive/002-sqlite-storage/data-model.md -> "`chats`"]

| Column | Type | Rules |
| --- | --- | --- |
| `jid` | TEXT PRIMARY KEY | full JID; server `s.whatsapp.net`, `g.us` or `lid`; ≤ 256 B |
| `phone` | TEXT NOT NULL DEFAULT '' | digits of the user part when the server is `s.whatsapp.net`, otherwise `''`; set by the store from `jid` |
| `name` | TEXT NOT NULL DEFAULT '' | ≤ 1 KiB; original text |
| `name_fold` | TEXT NOT NULL DEFAULT '' | `fold(name)` (R7) |
| `name_quality` | INTEGER NOT NULL DEFAULT 0 | 0 = none, 1 = fallback, 2 = real (push, business, group subject, history-sync display name), 3 = saved (address-book full or first name) |
| `name_time` | INTEGER | Unix ms when the name was seen; `NULL` while `name_quality = 0` |
| `last_message_time` | INTEGER | Unix ms; `NULL` = no activity known |

Indexes: `chats_last_activity ((last_message_time IS NULL), last_message_time DESC, jid)`,
`chats_name ((name_quality = 0), name_fold, jid)`, `chats_phone (phone)`. The first two are on the exact `ORDER BY`
expressions of the two chat sorts, so listing chats needs no sort step.

No index is partial. SQLite uses a partial index for `col = ?` only when it can prove from the query that the bound
value satisfies the index's condition, which it cannot for a parameter, so a partial index would leave those
lookups scanning the table.

Whether a chat is a group follows from the JID (`g.us`); it is not stored.

##### Name rule (FR-051)

An offered name `(name, quality, time)` replaces the stored one when the name is non-empty and
`(quality, time, name)` is greater than `(name_quality, name_time, name)` in row-value order. Quality is compared
first, then time, then the names byte-wise; a stored `NULL` time (no name yet) compares lowest. The result is a
total order, so it never depends on which offer
arrives first (SC-010). Empty names are rejected before the comparison. An offer without a time, or with one
outside [2000-01-01, 2100-01-01), is a validation error.

##### Last activity rule (FR-049, FR-050)

`last_message_time = max(last_message_time, offered)`; `NULL` offered never changes it.

Ingest field sources (no schema change): [Source: .specify/archive/005-message-ingest/spec.md -> "**Chat**: A direct or group conversation"] [Source: .specify/archive/005-message-ingest/data-model.md -> "Chat (`store.ChatUpdate`)"]

| Field | Source | Rule |
| --- | --- | --- |
| `JID` | normalised chat JID | |
| `Name`, `NameQuality`, `NameTime` | R12 order: history name, stored name, contact table, push name, group lookup, fallback | feature 002 quality, time and tie-break rule; never downgraded |
| `LastActivity` | set by `StoreMessages` from message times | only moves forward (S4) |


#### Message

The MCP latest-message projection uses `id`, `chat_jid`, `chat_name`, `sender` (full normalized JID), `sender_name` (local resolution/fallback), `timestamp` (UTC RFC 3339 with available fractional precision), `is_from_me`, `content`, `caption` and `media_type` (empty, image, video, audio or document). Text and caption are separate and may be empty. Latest is the actual row for that chat ordered by timestamp descending, then ID descending. This projection excludes `MediaMeta`, download URLs, keys, hashes, device state and raw internal structs; authoritative values survive safe text escaping. [Source: .specify/archive/006-mcp-stdio-tools/data-model.md -> "Message"]

identified by chat JID and ID; carries the sender (full JID and phone number), time, from-me flag, text,
caption, media type and media metadata. Belongs to exactly one chat. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Message"]

[Source: .specify/archive/002-sqlite-storage/data-model.md -> "`messages`"]

| Column | Type | Rules |
| --- | --- | --- |
| `chat_jid` | TEXT NOT NULL, FK → `chats(jid)` | part of the key |
| `id` | TEXT NOT NULL | 1–256 B, valid UTF-8, no control characters; opaque |
| `sender_jid` | TEXT NOT NULL | full JID without the device part; server `s.whatsapp.net` or `lid`; ≤ 256 B |
| `sender_phone` | TEXT NOT NULL DEFAULT '' | digits of `sender_jid`'s user part for `s.whatsapp.net`, else `''`; derived by the store (FR-047) |
| `time` | INTEGER NOT NULL | Unix ms in [2000-01-01, 2100-01-01) |
| `from_me` | INTEGER NOT NULL | 0 or 1 |
| `text` | TEXT NOT NULL DEFAULT '' | ≤ 256 KiB |
| `text_fold` | TEXT NOT NULL DEFAULT '' | `fold(text)` |
| `caption` | TEXT NOT NULL DEFAULT '' | ≤ 256 KiB |
| `caption_fold` | TEXT NOT NULL DEFAULT '' | `fold(caption)` |
| `media_type` | TEXT NOT NULL DEFAULT '' | `''`, `image`, `video`, `audio` or `document` (CHECK) |
| `media_url` | TEXT | ≤ 4 KiB; `NULL` = absent |
| `media_direct_path` | TEXT | ≤ 4 KiB |
| `media_key` | BLOB | exactly 32 B when present |
| `file_sha256` | BLOB | exactly 32 B when present |
| `file_enc_sha256` | BLOB | exactly 32 B when present |
| `file_length` | INTEGER | 0 to 4 GiB when present |
| `mime_type` | TEXT | ≤ 255 B |
| `file_name` | TEXT | ≤ 1 KiB; the original document file name |

Key: `PRIMARY KEY (chat_jid, id)`. Foreign key `chat_jid REFERENCES chats(jid)`, with no cascade: the merge moves
messages explicitly.

Indexes (S3, FR-053):

- `messages_chat_time (chat_jid, time DESC, id DESC)`: chat history, context, latest message per chat
- `messages_sender_time (sender_phone, time DESC, chat_jid DESC, id DESC)`: sender filters, contact chats and last
  interaction by phone number, in the listing order, so a single-identity sender query needs no sort
- `messages_sender_jid (sender_jid, time DESC, chat_jid DESC, id DESC)`: the same three by LID, and the LID merge's
  sender rewrite
- `messages_time (time DESC, chat_jid DESC, id DESC)`: listing across chats, `after`/`before`, in exactly the
  listing's order, so no sort step is needed

- `messages_id (id)`: a lookup by message ID alone (FR-055), which the `(chat_jid, id)` key cannot serve
- `messages_chat_sender_phone (chat_jid, sender_phone, time DESC, id DESC)` and
  `messages_chat_sender_jid (chat_jid, sender_jid, time DESC, id DESC)`: a listing filtered by both a chat and a
  sender, which would otherwise read every row of the chat (owner's decision, sixth design review; they add two
  index writes per stored message). They also serve the per-chat `EXISTS` probes of `ContactChats`

A sender under both identities is never matched with one `OR`: SQLite plans `sender_phone = ? OR sender_jid = ?` as a
`MULTI-INDEX OR`, which collects the row IDs of every message of the contact and sorts them, and with a chat filter
as a search on the chat alone. Every sender query is instead a `UNION ALL` of a phone branch and a LID branch, each on
its own index, with `ORDER BY`, `LIMIT` and `OFFSET` on the compound (owner's decision, 2026-10-30). Its plan is
`MERGE (UNION ALL)` over `SEARCH messages USING COVERING INDEX messages_sender_time (sender_phone=?)` and
`... messages_sender_jid (sender_jid=?)`, or the two chat-and-sender indexes with a chat filter. A time filter adds
`AND time<?` or `time>?` to each search, and search text makes the index non-covering (`USING INDEX`), since the
folded text is read from the row: a deep sender listing with search text reads every skipped row of the contact
and is linear by design, as a text search is. A contact with one identity runs only its branch. A plan with
`MULTI-INDEX OR`, or with a `SEARCH` on `messages_chat_time (chat_jid=?)` for a chat-and-sender listing, is rejected.

A query plan test (`EXPLAIN QUERY PLAN`) holds the expected plan of every query in FR-054–FR-066 and fails on any
other. Every lookup by key, chat, sender or contact is a `SEARCH` on one of these indexes. A text search, and a
listing with no chat or sender filter, walk `messages_time` newest first (`SCAN messages USING INDEX messages_time`)
and stop once the page is full. Context queries bind the hit's position as a row value, `(time, id) < (?, ?)` or
`> (?, ?)`, so their plan is `SEARCH messages USING INDEX messages_chat_time (chat_jid=? AND (time,id)<(?,?))`; a
plan with only `(chat_jid=?)` walks the chat from one end and is rejected. Joins to `whatsmeow_lid_map` always put
the map's key on its own side (`m.lid = <expression on the contact>`, `m.pn = ...`), so the plan is a `SEARCH` on
the map's key; a `SCAN m` is rejected. A temporary b-tree is expected only in the places this table lists, and never holds full
message rows:

| Query | Expected sort | Why |
| --- | --- | --- |
| `SearchContacts` | `USE TEMP B-TREE FOR GROUP BY` and for its `ORDER BY` | one row per person, built from both of the person's entries before the text is matched; holds contact rows, bounded by the device store's contact count |

No message query has a temporary b-tree: every listing, including a sender listing under both identities, walks
its indexes in the listing order. `ContactChats` selects the page's chat JIDs first and then reads those chats' rows, so its key query walks
`SCAN chats USING COVERING INDEX chats_last_activity` with
two correlated `SEARCH`es per chat, on `messages_chat_sender_phone (chat_jid=? AND sender_phone=?)` and
`messages_chat_sender_jid (chat_jid=? AND sender_jid=?)`, and has none either.

The order of every list is deterministic: messages by `(time, chat_jid, id)`, newest first; chats by `(last_message_time, jid)`
or `(name_fold, jid)`. Rows without a name or time come last.

##### Re-store rule (FR-048)

On `(chat_jid, id)` conflict every column takes the new value, except the eight media metadata columns. Each of
those keeps its stored value when the new one is `NULL`; an empty string or empty blob counts as `NULL`. The chat's
last activity follows the max rule above.

##### Validation (FR-075)

Validation happens before any SQL runs. The store rejects:

- empty or malformed identifiers and servers outside the allow-lists;
- invalid UTF-8 and control characters in identifiers;
- any field over its limit;
- a media type outside the list;
- media keys or hashes not exactly 32 B;
- a negative file length or one over 4 GiB;
- a time outside the range.

Text, caption and names may contain any valid UTF-8, control characters included. They are stored unchanged and
escaped only when they appear in diagnostics.

Ingest field sources and eligibility (no schema change): [Source: .specify/archive/005-message-ingest/spec.md -> "**Message**: A chat-scoped ID"] [Source: .specify/archive/005-message-ingest/data-model.md -> "Message (`store.Message`)"]

| Field | Source, live (`events.Message`) and history (`ParseWebMessage`) | Rule |
| --- | --- | --- |
| `ChatJID` | `Info.Chat`, normalised (R7) | phone, group or LID JID; no device part; never invented |
| `ID` | `Info.ID` | non-empty, at most 256 bytes; identity is `(ChatJID, ID)` |
| `SenderJID` | own message: the account's phone JID; group: `Info.Sender` (history: participant); direct: `Info.Sender`, normalised | device part dropped; an absent or invalid sender rejects the message |
| `Time` | `Info.Timestamp` | 2000-01-01 to 2100-01-01 UTC; zero or outside rejects the message |
| `FromMe` | `Info.IsFromMe` | |
| `Text` | `Conversation`, else `ExtendedTextMessage.Text` | at most 256 KiB, valid UTF-8 |
| `Caption` | image, video or document caption | at most 256 KiB; never copied into `Text` |
| `MediaType` | `image`, `video`, `audio`, `document`, or empty | audio has no caption |
| `Media` | URL, direct path, media key, file SHA-256, encrypted SHA-256, length, MIME type, document file name | store limits; 32-byte keys and hashes |

`SenderPhone` is derived by the store from a phone `SenderJID`; it stays empty for an unmapped LID.

A message is **eligible** when its chat is a user, group or LID JID, it has text or a recognised media type, it is
not an edit (`IsEdit`), its raw content is not a bare `ProtocolMessage`, and, in history, `ParseWebMessage` kept the
ID of `webMsg.Key.ID`. Every other message, including status broadcasts and newsletters, is ignored without a
write. An eligible message that fails `store.ValidateMessage` is **rejected**: skipped,
counted, never repaired.


#### Media metadata

what is needed to download a message's media later: URL, direct path, key, hashes, length, MIME
type and original file name. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Media metadata"]

#### Contact

The MCP contact projection has a canonical person `jid` (mapped phone or unresolved LID), `phone_number` (normalized digits or null), and `name`. Exclude group and unsupported-domain rows; group aliases before matching/paging and allow every name field on either alias to match. Choose full, first, push, business name, then identity fallback within an entry; prefer a mapped phone entry's chosen name over its LID alias. A mapping does not authorize an account action. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "Contact"] [Source: .specify/archive/006-mcp-stdio-tools/data-model.md -> "Contact"]

a person as known to the device store (names and JID, with the LID-to-phone map). Read by the store,
written only by whatsmeow. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Contact"]

The device-owned `whatsmeow_lid_map` links LID and phone user parts. Ingest reads it but never writes it; the library owns updates. Existing merges are not undone on reassignment. [Source: .specify/archive/005-message-ingest/spec.md -> "**Identity mapping**: An association"] [Source: .specify/archive/005-message-ingest/data-model.md -> "Identity mapping (`whatsmeow_lid_map`, device-owned, read only here)"]

#### Device Session

The one saved linked-device identity and credentials for the selected data directory; may exist locally while offline or be revoked by the phone. [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> "Device Session"]

[Source: .specify/archive/003-whatsapp-pairing-status/data-model.md -> "Device Session and Generation"]

Use the existing whatsmeow device row, keys and session-specific/global interfaces. No new copy of credentials is stored. Owner generations add only process-local control state:

| Field | Meaning and constraint |
| --- | --- |
| epoch | Monotonically distinct within one owner; stale epoch events never change current state. |
| device/client | At most one active pair; fresh device after confirmed revocation, same shared container. Never overlap a new client with unquiescent old access. |
| lifetime | Cancellable generation context, not a 30-second expiring connection context. |
| admission | Per-generation reject/drain plus the one shared store gate. Nested transaction calls reuse a live lease. |
| phase | idle, connecting, pairing, connected, disconnected, logged_out, retiring, storage_failed, stopping or stopped. |
| storage health | Separate from physical connection; startup repair pending, guarded-write fault and ready are internal, not extra login-tool fields. |

Confirmed logout permits revocation cleanup; separately authorized offline recovery also permits local identity removal under FR-099. A new unpaired client may be created only when the recovery barrier permits it, after the prior window drains and a reload confirms no saved identity exists. Ordinary disconnect preserves credentials and logged-in state. Save/storage failure cannot count as successful durable linking. Forced termination/disk failure is not described as clean recovery.

Guard interface fields are installed before client creation and stay stable throughout the generation. Initialized denotes interface wiring, not durable identity; ordinary pre-ready calls fail fast before gate admission/SQL until first Save succeeds, rather than exhausting slots waiting for Save. Private raw delegates may bind on that Save's validated JID without being published in device fields. Client delete requests retire the generation and return deferred cleanup rather than allowing the library to reset live fields; confirmed-revocation SQL cleanup happens after drain. Failed pairing preserves any saved identity for explicit verification rather than silently deleting it.

Failed revocation persistence blocks new generations in the current owner, not safe finalization forever: on explicit stop, close/unlock only after true quiescence and exit unsuccessfully with an unresolved-state warning. The previously armed unsafe-run marker blocks automatic account work on restart; no promise of persisted deletion is made when storage rejected it. Recovery must not infer that uncertainty proves revocation.


Quiet-stop generation extension: [Source: .specify/archive/004-shutdown-drain/spec.md -> "Generation"] [Source: .specify/archive/004-shutdown-drain/data-model.md -> "Generation (extended)"]

One client instance and its connection: an owner `genState` plus its `Session`.

| Field | Meaning | Rule |
| --- | --- | --- |
| `epoch` | the owner epoch of this generation | unchanged from feature 003 |
| `accountWork` | a production Connect was attempted on this client | set before `ConnectContext`; when false, no check is needed |
| `fence` | the generation's network block | one per generation; never shared with another generation |
| `barrier` | the store's device barrier | unchanged; gains a refusal count |
| `droppedEvents` | owner-relevant events dropped after retirement, except `Disconnected` and message events covered by FR-138 | counted from the moment the retire bit is set; FR-138 messages go to `DroppedMessages` instead |
| `lateCalls` | calls from the program's own code to the session after the retire bit is set, except the retirement sequence's own Disconnect | counted from the same moment |
| `work` | the program's own work: the pairing window's QR channel, the repair pass; never a Connect context | a child of the owner context; cancelled by the stop signal and at the start of retirement |
| `lib` | the library's `BackgroundEventCtx` and the parent of the attempt context its handlers run with | created as `context.WithCancel(context.WithoutCancel(o.ctx))`; cancelled only after the fence and the barrier (invariants I8); never by the stop signal, `work` or a watchdog once Connect has returned |
| `quiet` | the generation's `QuietStopResult` | written once, by retirement |

**States.** open → connecting / connected / pairing → retiring → retired (blocks closed, drained) → checked (result
known) → released. A generation never returns to an earlier state. A new Connect never runs on a retired generation.

#### Connection Status

A consistent snapshot of connection, login, active-pairing availability and selected WhatsApp Web version, distinguished from the standalone offline snapshot. [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> "Connection Status"]

[Source: .specify/archive/003-whatsapp-pairing-status/data-model.md -> "Connection Status"]

| Field | Constraint |
| --- | --- |
| connected | True only after the current generation's Connected confirmation; false on disconnect/logout/stop. |
| logged_in | Locally saved identity unless confirmed revoked: false immediately on confirmed logout, preserved on ordinary disconnect. Revoked-device retirement/cleanup must finish before a new generation. Not proof of current network acceptance. Fresh PairSuccess may establish saved identity, but pair command still waits for Connected. |
| pairing_required | Derived: not logged_in AND current code exists AND now is before its expiry. False after code expiry even if a window awaits replacement. |
| wa_version | Selected immutable candidate rendered as a three-part version, not a claim of server negotiation. Standalone status uses the bundled version only and labels it unrefreshed. |

Return a copied coherent snapshot. Standalone status always connected=false/pairing_required=false, with an offline-credentials warning. Do not expose device JID, keys, QR or internal recovery ticket in ordinary status.

#### Pairing Attempt

A single owner-requested linking window that may supply multiple replacement codes, retaining only the current code and its expiry. A code's expiry does not end the attempt; final timeout, cancellation, logout or success ends it and invalidates its QR and delayed events. [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> "Pairing Attempt"]

[Source: .specify/archive/003-whatsapp-pairing-status/data-model.md -> "Pairing Attempt"]

| Field | Constraint |
| --- | --- |
| attempt identity | Bound to the owner epoch; one attempt at a time. |
| approval | Explicit request opens one window including upstream replacement codes, never a new window automatically. |
| current code | At most 4 KiB; empty/malformed/over-limit/encoder-failed input cannot replace valid state. |
| current representation | At most one derived terminal/image representation; no QR history, files or logs. |
| expires_at | Earlier of upstream expiry and 60 seconds from receipt; use monotonic elapsed time internally. |
| result | active, succeeded, timed_out, cancelled or failed. Terminal outcomes invalidate code and late events. |
| consumer | Exactly one fast channel consumer; callers share the snapshot/update signal rather than reading the upstream channel. |

Code expiry is not final attempt timeout. An unresolved final timeout retains unsafe state and requires explicit offline recovery before a separately approved new pairing; Enter, EOF and cancellation do not authorize a new attempt or recovery. Request wait-context expiry does not silently grant/cancel another caller's shared window; explicit owner cancellation controls the attempt.

A client generation is bound to its logical pairing window; retire/drain it before a different window so untagged old events cannot masquerade as the new attempt. Reload durable identity after drain to avoid overwriting it, not to authorize reuse: an unresolved attempt remains blocked by FR-099 even if a late identity is found. Version recovery inside a still-approved window remains subject to the existing 405 budget, never authorization to reopen a terminal window.

#### Version Selection

The bundled fallback and the latest successfully discovered WhatsApp Web version, used on the next connection. [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> "Version Selection"]

#### Recovery Budget

Up to three delayed outdated-client retries since the last confirmed connection; distinct from pairing requests and ordinary network recovery. [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> "Recovery Budget"]

##### Shared version and retry invariants

[Source: .specify/archive/003-whatsapp-pairing-status/data-model.md -> "Version Selection and Recovery Budget"]

- Version is a value triple of uint32 components. Discovery accepts the pinned `2.3000.<uint32 decimal>` grammar, including revision zero; no invented version floor. Preserve bundled fallback and latest successful candidate in memory; no version persistence/migration.
- Handshake freezes one candidate and registration hash together in a fresh cloned payload. Library globals remain immutable. Refresh never changes an in-flight payload or disconnects a healthy socket.
- One version request in flight, 15 seconds, at most 1 MiB actual application-body consumption, complete response required, redirects/TLS errors rejected. Unknown-length bodies reaching the budget without proving completion are conservatively rejected, never accepted as prefixes.
- The 405 budget is three retries after the initial rejection, each five seconds and a refresh attempt; duplicate events share it. Only current Connected resets it. Ordinary network backoff is separate and cannot bypass terminal state.
- Saved-session shell startup recovery has a distinct budget of three retries after the initial failure/timeout, each after a cancellable five-second wait. Connected, shutdown, logout, storage fault or 405 handoff ends that sequence. Exhaustion retains identity and a disconnected shell; it never starts pairing or replenishes the 405 budget.
- Each owner-initiated initial, explicit, startup-retry, 405 or 515 connection attempt has a 30-second watchdog; timeout cancels and drains before another attempt. A still-undrained call retains the single attempt slot and ownership. A healthy connection is not bounded by that watchdog.

These budgets remain desired policies, not guarantees of retry availability. Default deny startup retry, 405, 515 and ordinary upstream reconnect until the path-specific detached-handler proof and current owner review in the connection-owner contract and all applicable gates pass. Never Connect on or after retirement of an upstream generation whose drain is unproven, including a replacement client; otherwise remain disconnected and report the limitation. The one exception is the owner-approved post-pair reconnect of `pair` (see the connection-owner contract). The budgets are not delivered by feature 003; their implementation is deferred to the follow-up in the roadmap.

#### Unsafe-Run Marker

[Source: .specify/archive/003-whatsapp-pairing-status/data-model.md -> "Unsafe-Run Marker"] [Source: .specify/archive/004-shutdown-drain/spec.md -> "Run marker"] [Source: .specify/archive/004-shutdown-drain/data-model.md -> "Nothing new is stored on disk"]

The schema-3 format is unchanged and a clean marker following a quiet stop is identical to one from a run without a production connection.

Application migration 3 adds exactly one bounded store-level marker with clean/unsafe states and a five-bit set of repair collections whose device-specific markers would otherwise be lost on identity deletion. A new database known to have been created under the lock starts clean; an existing schema 1/2 database initializes unsafe because its previous run cannot be classified, even if no device row exists. If creation or migration was interrupted before the marker was committed, the reopened database is existing/unknown, never reconstructed as freshly created. Missing, malformed, unreadable or unsupported marker state is never clean. A committed unsafe state precedes all account-capable work and remains armed across all generations until a permitted clear. Without a production Connect attempt, automatic clearing still requires true drain and durable completion of any confirmed-revocation cleanup. After such an attempt, only an orderly stop meeting FR-110 may clear automatically. If the marker stays unsafe, recovery still requires separate authorization and the verification and removal prerequisites below. An arm error or ambiguous commit prevents automatic clear in that invocation; reopen and inspect the committed state. The marker is not proof of logout, identity validity or phone-side unlink; clean/unsafe and the pending repair bitset are independent. [Source: .specify/archive/004-shutdown-drain/spec.md -> FR-003]

The marker schema enforces one row with primary key `id = 1`, non-null state limited to `clean` or `unsafe`, and a non-null integer repair bitset from 0 through 31. The bit positions correspond exactly to the five existing supported `device_repair` collections; migration and tests must use one documented stable mapping. Status inspection validates the singleton and all constraints without writing. A missing or malformed schema-3 row is classified unsafe/corrupt and is not automatically recreated because the pending bits may be unknown.

An opened fresh-pairing attempt stays unresolved until it has both a durably saved identity and a current-generation Connected confirmation. Timeout, failure, cancellation or an orderly stop before both conditions retains unsafe state: the phone may have linked a device even when no local row exists. PairSuccess alone is insufficient. Subsequent automatic pairing or saved-identity reuse is blocked until offline recovery.

Recovery uses the explicit, interactive offline branch of `pair`: the operator verifies the named account/device and any orphaned link, performs separately authorized phone-side unlink if needed, and separately authorizes removal of the local identity if present. Removal and marker clearing have atomic or equivalent safe ordering; no network or fresh QR occurs in that recovery action, and no chats/messages are deleted. Refusal, EOF, unverified phone state or definitely failed persistence keeps the marker unsafe. An ambiguous commit exits unsuccessfully and requires reopening to inspect committed state; clean can appear only after the authorized prerequisites were met. A later explicitly approved pair is separate. Process/OS/power failure coverage requires verified durable synchronization, not WAL `synchronous(NORMAL)` alone; physical corruption, storage that violates flush guarantees and restored older backups remain outside the guarantee.

Before any genuine confirmed-revocation deletion or separately authorized offline identity removal, atomically union that device's pending `device_repair` collections into the store-level bitset in the same transaction as deletion, before foreign-key cascades remove the original rows. Retain the bitset across marker clearing, a missing identity and restart. When a new identity is saved, materialize the pending collections as new-device repair needs with unknown target and valid revision in the same transaction as first Save, or keep ordinary app-state admission closed until materialization commits. This closes the interval between Save and marker creation while still permitting the generation-valid key delivery needed for repair. Fence and clear each need only after successful current repair, never merely after pairing. Interrupted transfer/materialization cannot lose a bit or falsely declare recovery complete. Device session keys removed by separately authorized identity removal or genuine revocation are distinct from keys protected against deletion by app-state repair.

#### Durable Repair Metadata

[Source: .specify/archive/003-whatsapp-pairing-status/data-model.md -> "Durable Repair Metadata"]

Application migration 2 adds `device_repair`; migration 3 adds the singleton unsafe-run marker. Device schema 16 and chats/messages remain unchanged. Fresh stores run migrations 1 through 3; an existing schema 1/2 database upgrades to schema 3 with unsafe state. Inspection of an old readable schema remains read-only and reports legacy safety unknown. No Go migration file is written during this planning phase.

| Column | Constraint |
| --- | --- |
| device_jid | Required, canonical saved device identity, at most 256 bytes; foreign key to whatsmeow_device.jid with ON DELETE CASCADE. |
| collection | Required, exactly one of critical_block, critical_unblock_low, regular_high, regular, regular_low. |
| revision | Required positive signed 64-bit integer; increments before each protected mutation; overflow fails without resetting live state. |
| target_version | Nullable fixed 8-byte big-endian encoding of the protocol uint64 version; NULL means reset/unknown rather than an integer-domain truncation. |
| repair_ticket | Nullable fixed 16-byte non-secret uniqueness token for an owner-controlled repair; do not log it. Ordinary mutations do not inherit another repair's ticket. |
| updated_at_ms | Required UTC Unix milliseconds, diagnostic only, never a correctness/ordering substitute for revision. |

Primary key is (device_jid, collection). At most one row per five known collections for the owned device; no message bodies, contact snapshots, credentials or unbounded error strings. Unknown collection/JID/input is rejected before marker or data writes. Marker fields do not define a new total database quota.

#### Repair Coordinator

[Source: .specify/archive/003-whatsapp-pairing-status/data-model.md -> "Repair Coordinator"]

| Field | Constraint |
| --- | --- |
| admission_mode | Before connecting with pending repair, ordinary untagged app-state progress/MAC and bulk-snapshot calls fail fast; tagged repair and sync-key delivery remain permitted. Resume ordinary sync only after required recovery succeeds. |
| actual_call | At most one owner-invoked FetchAppState call is outstanding across client generations; timeout/retirement does not release its slot before that invocation returns. |
| deadline_outcome | Ten-second caller wait; cancel/invalidate the repair ticket, retain the marker, discard late results, and start no replacement FetchAppState call while the prior invocation remains outstanding. |
| deferred_sync | Coalesce deferred ordinary sync demand in a five-collection bitset; replay through the same FetchAppState slot after repair, never as another full-resync retry or an unbounded per-notification goroutine. |

The SDK's appStateSyncLock is a plain mutex before its context-aware calls. A context alone cannot bound that wait. Startup admission mode is established before NewClient/Connect, not merely after an error: untagged background FetchAppState then fails its first guarded progress call before SQL/HTTP, rather than holding that mutex across network sync ahead of repair. Expected deferral does not become a storage fault or consume a repair attempt. Never block sync-key delivery needed by a tagged repair. A dirty marker created during otherwise successful ordinary sync does not by itself switch admission mode.

The controller waits on result/cancellation/deadline, not directly on an uninterruptible SDK call; its one actual-call lease remains held until that call returns. Late work sees an inactive ticket/cancelled context and cannot clear a marker or mutate through a retired guard. Deferred ordinary requests are coalesced/replayed as ordinary incremental sync after repair, not extra full-resync attempts; failure leaves demand pending for a new notification/connection event, never a tight autonomous retry loop. A guarded-write fault during active use pauses/cancels that generation and requires an operator-authorized restart, whose pending markers activate startup admission before competing SDK work. Missing sync keys are transient: retain pending state and allow key delivery, without repeatedly retrying full resync.

The SDK spawns missing-key requests with context.WithoutCancel: their ctx has no caller deadline/cancellation. They are SDK-owned protocol jobs, not outstanding FetchAppState invocations; do not invent an owner tail timer or claim the ten-second caller budget bounds them. The pin has a default 75-second message-response wait, per-key 24-hour request dedup and disconnect-cleared response waiters/retryFrame connection wait up to five seconds; these are component behaviours, not a hard total-tail/RSS guarantee. On stop disconnect before finalization and deny/cancel all old-generation device operations even for detached contexts. Inactive repair tickets forbid protected progress/clear, not necessary generation-valid key-request crypto/key delivery, which still uses fresh shared operation budgets. Never free a FetchAppState slot before that actual invocation returns or clear on late success.

#### Network fence

| Field | Meaning | Rule |
| --- | --- | --- |
| `closed` | the fence is closed | set once, after the library's own Disconnect |
| `conns` | the wrapped connections dialled by this generation | all closed when the fence closes |
| `refusedDials` | dials attempted after closing | counted, never performed |
| `refusedWrites` | writes attempted on a wrapped connection after closing | counted, never sent; not part of the check (the connection is already closed, and TLS and HTTP/2 teardown write on it) |

Reads that fail after closing are not counted (research R3).

[Source: .specify/archive/004-shutdown-drain/data-model.md -> "Network fence"] [Source: .specify/archive/004-shutdown-drain/spec.md -> "Network block / storage block"]

#### Storage refusal count

`DeviceBarrier.MarkRetiring()` starts the count when retirement starts. `DeviceBarrier.Refused() uint64` is the number
of guarded calls for this device session that failed after that point for a reason that comes from the guard or the
context: `ErrRetired`, a context error, a stale lease or another guard error. A result the database itself returns
(for example "not found") is not counted. It is monotonic, and is read only after `Retire` has drained the admitted
work.

A read turned away with `ErrRetired` (after `Retire`, so after the network fence closed) is not in `Refused()`; it is
counted in `DeviceBarrier.RefusedReads() uint64`, which the check does not use. A read that fails on its context, a
stale lease or another guard error stays in `Refused()`. Whether a call is a read is the guard's own `write` flag.

[Source: .specify/archive/004-shutdown-drain/data-model.md -> "Storage refusal count"]

#### QuietStopResult

| Field | Type | Meaning |
| --- | --- | --- |
| `Passed` | bool | true only when every condition of FR-109 holds |
| `Reason` | enum | `passed`, `work_running`, `network_refused`, `storage_refused`, `call_refused`, `event_dropped`, `stop_bound_exceeded`, `observation_incomplete`, `observation_error`, `not_checked` |
| `LibraryGoroutines` | int | client-library goroutines left at the last observation, after removing allow-listed work |
| `NetworkRefused` | uint64 | refused dials |
| `StorageRefused` | uint64 | the barrier's count (refusals, context errors, stale leases, guard errors) |
| `CallsRefused` | uint64 | late calls to the session (see Generation) |
| `EventsDropped` | uint64 | dropped owner-relevant events counted by `droppedEvents`; excludes `Disconnected` and FR-138 message events |
| `DroppedMessages` | uint64 | FR-138 message events dropped after retirement; reported as a possible gap without failing the quiet-stop check [Source: .specify/archive/005-message-ingest/contracts/ingest.md -> "Revised event-count rule"] |
| `Duration` | duration | from the start of retirement to the decision |

When several conditions fail, `Reason` names the first in this order: `observation_incomplete`,
`observation_error`, `stop_bound_exceeded`, `storage_refused`, `network_refused`, `call_refused`, `event_dropped`,
`work_running`. The counts report all of
them. `not_checked` is used for a generation that never attempted a Connect. The result never holds dump text,
function names, argument values, keys or account identifiers.

[Source: .specify/archive/004-shutdown-drain/data-model.md -> "QuietStopResult"] [Source: .specify/archive/004-shutdown-drain/spec.md -> "Quiet-stop result"]

#### Allow-list entry

| Field | Meaning | Rule |
| --- | --- | --- |
| `Function` | one exact function name as `runtime.Stack` prints it | never a package or module prefix |
| `ReviewedFor` | the `go.mau.fi/whatsmeow` version the entry was reviewed against | must equal the `go.mod` require line, or a test fails |
| `Evidence` | why the work is harmless after both blocks | a reference to `verification.md` |

The list starts empty (research R5).

[Source: .specify/archive/004-shutdown-drain/data-model.md -> "Allow-list entry"] [Source: .specify/archive/004-shutdown-drain/spec.md -> "Allow-list"]

#### History sync

WhatsApp-supplied conversations and past messages, distinct from live delivery. One worker per generation downloads and processes bounded chunks; history is not emitted as a live event. [Source: .specify/archive/005-message-ingest/spec.md -> "**History sync**: WhatsApp-supplied conversations and past messages"] [Source: .specify/archive/005-message-ingest/contracts/ingest.md -> "History worker"]

#### Per-generation ingest state

| State | Bound | On overflow |
| --- | --- | --- |
| History queue | 8 notifications | the notification is reported as incomplete history; no disconnect |
| History worker | 1 goroutine; one decoded history at a time (≤ 128 MiB expanded, fork limit) | – |
| History flush | 250 messages or 4 MiB of field bytes, ingest lock released between flushes | the batch is cut early |
| Name cache | 4,096 chats, least recently used | the oldest entry is forgotten; the next miss reads the store again |
| Contact re-read queue | 4,096 chats (the name cache's size), each queued once; at most 3 re-reads per chat, 60 s, 5 min and 30 min after the previous read; at most 8 chats re-read per live message or history flush; no goroutine | a chat is dropped, not queued, when the queue is full; its name stays until the next start |
| Group lookup set | 64 distinct groups; 1 worker; 10 s per lookup | the request is dropped and counted; the fallback name stays |
| Failed-lookup cache | 1,024 groups, entries expire after 10 min | the oldest entry is forgotten; that group may be asked again sooner |
| LID hint set | 1,024 LIDs; 1 worker; 5 attempts 1 s apart | the hint is dropped and counted; startup reconciles it |
| Reconcile cursor | one LID: where an unfinished startup pass stopped; the worker pages on from it, 200 per page, between hints | – |
| Ingest lock | one per generation | – (serialises message writes, history batches and merges) |
| Counters | `stored`, `ignored`, `rejected`, `unavailable`, `droppedMessages`, `historyIncomplete`, `lookupsDropped`, `hintsDropped`, `merged`, `mergeFailed` | `uint64`, logged at stop and on terminal failure |
| Re-lookup under the ingest lock | one 2 s deadline per message write or history flush, at most 250 distinct LIDs | the remaining LIDs are written as LIDs; startup merges them |

[Source: .specify/archive/005-message-ingest/data-model.md -> "Per-generation ingest state (in memory)"]

#### Chat Page

Bounded, stably ordered chat or contact page: `chats: Chat[]` or `contacts: Contact[]`, effective integer `limit` and `page`, and returned `count` equal to array length. Empty arrays are `[]`; no total-match count. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "Chat Page"] [Source: .specify/archive/006-mcp-stdio-tools/data-model.md -> "Result Envelopes"]

#### Tool Result

Authoritative public data and faithful safe text, no QR or device credentials. Success has `source: "local_store"` and `untrusted_data: true`, plus a page or `chat: Chat`. Errors use `isError: true` with `error: {code, message, field?}` and no partial success. No unknown output properties. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "Tool Result"] [Source: .specify/archive/006-mcp-stdio-tools/data-model.md -> "Result Envelopes"] [Source: .specify/archive/006-mcp-stdio-tools/data-model.md -> "All success roots are objects"]

#### MCP Session

New, initializing, active, stopping, stopped. Initialize once; no reattachment or pump replacement. Unsafe storage/run-marker startup does not initialize. Account disconnect without logout does not erase stored data or block already-permitted local reads. [Source: .specify/archive/006-mcp-stdio-tools/data-model.md -> "Session"]

#### MCP Request

Frame check, credit admission, SDK dispatch, handler, pending response, written. A tool holds credit through response completion. Excess work fails without dispatch/store admission. On cancellation or transport failure retain accounting through actual handler completion and output disposal. [Source: .specify/archive/006-mcp-stdio-tools/data-model.md -> "Request"]

#### Tool Store Snapshot

No waiting state: admit only with no ordinary waiter, a free slot and reader, WAL at most 64 MiB and at least 640 MiB free. Cancel at 5 s or earlier on ordinary waiters, reader contention, WAL guard or caller cancellation. Hold accounting and admit no replacement until active SQLite work really returns its connection; latch unsuccessful shutdown if cancellation persists through the 5 s admission-wait bound. [Source: .specify/archive/006-mcp-stdio-tools/data-model.md -> "Store Snapshot"]

#### Stdio Pump Lifetime

One input frame and one output frame in two protocol pumps and one stderr pump. Ordinary diagnostics use a 16 × 4 KiB discardable queue; mandatory diagnostics use 16 × 1 KiB, a 5 s complete-write acknowledgment deadline and at most 128 fixed count/time cells. No retained raw query, name, body or client key. Overflow/failure/late acknowledgment latches unsuccessful shutdown independently of EOF and marker proof. Pumps hold bytes and completion channels only; lifecycle tracks mandatory acknowledgment on one earliest-deadline timer. A blocked inherited OS stream may remain until process exit after real work drains; cooperative test streams can join. [Source: .specify/archive/006-mcp-stdio-tools/data-model.md -> "Pump Lifetime"]

#### MCP Input Validation

Arguments are JSON objects; explicit null, unknown/duplicate keys, type coercion and fractions/exponents not safely representable as integers are rejected. `query` is capped at 1,024 UTF-8 bytes; require a non-whitespace character for contact search but preserve supplied text. Chat query defaults to empty; apply Unicode NFC/case folding with literal matching and search phone digits without treating search text as an identity. Contact identities accept 1–20 digits, supported formatting, phone JIDs and LID JIDs, up to 256 UTF-8 bytes; strip supported contact device suffixes and reject groups or unsupported domains. Exact chat JIDs also have a 256-byte limit and reject device-specific addresses. `page` accepts 0–100,000 only, subject to checked `page * limit` and a 100,000 offset ceiling. Keep store limits: IDs/JIDs 256 bytes, names 1 KiB, content and caption 256 KiB each, 32 MiB aggregate. Check device-origin names before scanning/folding; reject invalid legacy fields instead of truncating. [Source: .specify/archive/006-mcp-stdio-tools/data-model.md -> "All arguments are JSON objects"] [Source: .specify/archive/006-mcp-stdio-tools/data-model.md -> "`query`:"] [Source: .specify/archive/006-mcp-stdio-tools/data-model.md -> "Contact identity inputs accept"] [Source: .specify/archive/006-mcp-stdio-tools/data-model.md -> "`chat_jid`:"] [Source: .specify/archive/006-mcp-stdio-tools/data-model.md -> "`limit`:"] [Source: .specify/archive/006-mcp-stdio-tools/data-model.md -> "Store field ceilings remain binding"]

## Storage Data Flow

[Source: .specify/archive/002-sqlite-storage/data-model.md -> "Derived values"]

| Value | Definition |
| --- | --- |
| Sender name (FR-057) | For `from_me = 0`: the device-store contact for `sender_jid`, then for its counterpart through `whatsmeow_lid_map` (user part looked up, suffix added) (`full_name`, then `first_name`, `push_name`, `business_name`), else the saved or real name (`name_quality >= 2`) of the chat whose `jid = sender_jid`, else `''`. The chat is looked up under the sender's own JID only, not under its counterpart: a LID sender whose name sits only on the phone-number direct chat has no name until `MergeLID` moves the two together (owner decision 2026-10-30). For `from_me = 1`: `''` (the tools render "Me"). |
| Chat name on a message | `chats.name` of `chat_jid` |
| Latest message of a chat | the first row of `messages_chat_time` for that chat |
| Contact identity (FR-061) | the device's `whatsmeow_contacts` rows, joined with `whatsmeow_lid_map (lid, pn)`. A LID row with a mapped phone and the phone row are one person, keyed by the phone JID. The name comes from the phone row, else from the LID row, each in the order full, first, push, business. Groups (`g.us`) are excluded. |
| Resolved contact (FR-068) | the input as a phone number, phone JID or LID JID, plus its counterpart from `whatsmeow_lid_map` (`pn = <digits>` gives `lid`, `lid = <user part>` gives `pn`): `{phone, phoneJID, lidJID}`, where `phoneJID = phone || '@s.whatsapp.net'` and `lidJID = lid || '@lid'`; any may be empty but not all. Message matches use a phone branch (`sender_phone = phone`) and a LID branch (`sender_jid = lidJID`), each left out when its value is empty and joined by `UNION ALL` when both exist (§ Indexes), never one `OR`. The direct chats are `jid IN (phoneJID, lidJID)`: both exist only before a merge. `DirectChat` returns the phone chat, else the `@lid` chat. |
| Current device | the single row of `whatsmeow_device`. If there is none (never paired, or logged out), contact queries return no rows and sender names fall back to chat names. Its own number and LID are the user parts of `jid` and `lid` without a device part; `lid` can be `NULL` or `''`, and an empty own value never equals anything, so an unmapped contact's empty LID is never taken for the account. |

[Source: .specify/archive/002-sqlite-storage/data-model.md -> "State transitions"]

```text
missing ──create (private file)──▶ empty ──device upgrade──▶ device@16 ──migration 1..N──▶ ready
   any ──preflight refusal──▶ refused (no file changed)
   device@k ──device step fails──▶ device@k (step rolled back), no message migration, startup fails
   messages@k ──migration k+1 fails──▶ messages@k, startup fails, retry resumes from k
ready ──Close──▶ closed (WAL checkpointed by SQLite on last close)
```

A LID moves through the merge as follows, in one transaction: every message with `sender_jid = L`, in any chat,
gets `sender_jid = P` and the phone filled in; then, if chat `L` exists, `P` takes every message and `L` is gone.
`L`'s name is offered to `P` by the name rule only when its quality is saved or real; a fallback is dropped.
Repeating the merge once nothing is left under `L` does nothing.

## Connection and Recovery Data Flow

### Marker and Repair Transitions

[Source: .specify/archive/003-whatsapp-pairing-status/data-model.md -> "Marker and Repair Transitions"]

1. Before a protected version/MAC/bulk-contact mutation, validate the call, commit an upsert/increment of its marker, then delegate under the same admitted operation. If recording fails, do not mutate the protected state. If later work fails or crashes, marker remains.
2. Separate sibling methods may already have committed. A marker records uncertainty; it does not roll them back. Ordinary successful updates may conservatively leave a marker for next-start verification.
3. After an authorized connection, process startup-pending/failed collections through one serialized worker, at most one automatic attempt per collection per owner generation. Do not consume a gate lease while waiting for the network. Reuse the 10-second operation budget and 16-MiB external-body budget.
4. Prepare a repair ticket; propagate it through the actual full-resync call. Track every guarded-write error even when upstream only logs it. Unrelated/interleaved mutations replace/invalidate the ticket/revision so an old completion cannot clear new work.
5. Clear by compare-and-delete only after successful call completion, no guarded errors, current generation and an admission fence proving no superseding/in-flight protected mutation. If fencing/CAS fails, retain the row; no silent second repair loop.
6. Timeout/failure keeps durable state and internal recovery-pending health. If the run marker is unsafe, separately authorized offline recovery and fresh pairing precede a new connection; transferred needs survive identity removal and materialize for the new device. Never automatically unpair, evict cryptographic state for app-state repair, delete chats/messages or declare readiness on Connected alone.

The marker-admission/clear fence must not deadlock nested Signal transactions or hold a transaction/SQL writer across network calls. Fault-injection and interleaving tests are part of the acceptance contract, not assumed proof.

### Store and Stop States

[Source: .specify/archive/003-whatsapp-pairing-status/data-model.md -> "Store and Stop States"]

| State | Invariant |
| --- | --- |
| open | Existing accepted shared budgets apply to device and application operations. |
| generation retiring | Old client cannot admit new calls; admitted calls drain. Shared store remains available for controlled revocation/fresh generation only after old drain. |
| store closing | Shared admission permanently closed; reject new calls; contexts cancelled after grace. Pools remain open while work is active. |
| quiescent | Zero admitted operations and no future admissions; safe to close pools. Completion signal closes once. |
| closed | Pools closed after quiescence; lock release may follow. Close errors are reported; timeout alone never establishes this state. |

Reuse 16 active/64 waiters, five-second admission, ten-second operation, 16-MiB input/32-MiB output and existing disk reserve/resume profile. Reuse appropriate field/aggregate budgets, not an unrelated total-address-book count. Contact/LID device views and session migration retain no private raw cache; raw migratedPNSessionsCache is not an excepted device-store control. Unused all-contact/all-key exports fail explicitly rather than truncate or materialize unlimited results.

E003-RX-01 applies only to documented upstream receive resource exposure. It does not relax these store-state or marker invariants.

### Quiet-Stop Clear Decision

The existing `finishStop` decision, with one more input:

```
clear = guardArmed
        && !unresolved            // no unresolved pairing attempt (FR-110)
        && !unsafeStop            // set by a failed check of a generation that attempted Connect, among the existing causes
        && (!checked || withinStopBound)  // a generation that attempted Connect was decided before stopBound passed; a run with no production Connect is unaffected (feature 003's safe-clear rules)
        && !health.StorageFault
then ClearRunGuard, which still enforces low-disk admission
```

`upstream_drain_unproven` no longer sets `unsafeStop` on its own. A failed check does, for that generation. [Source: .specify/archive/004-shutdown-drain/data-model.md -> "Run outcome (owner)"]

### Ingest state transitions

```text
running ──(message-store error)──────────────▶ failed  (terminal: no acks, workers stop, owner signalled)
running ──(retire bit, step 2)───────────────▶ retiring (no acks, drops counted, workers stop at checkpoints)
failed  ──(retire bit)───────────────────────▶ retiring
retiring ──(workers and callbacks joined)────▶ stopped
```

`failed` and `retiring` never return to `running`. A new generation starts in `running` with empty state.

[Source: .specify/archive/005-message-ingest/data-model.md -> "Ingest state transitions (one generation)"]

The local stdio data path checks frames and request credit before SDK dispatch, uses the read-only store and local aliases in one snapshot, then preflights and writes the structured result plus safe JSON text. Release credit after actual output completion; on failure stop the session and join actual tool and account work before closing storage and releasing the lock. [Source: .specify/archive/006-mcp-stdio-tools/data-model.md -> "Request"] [Source: .specify/archive/006-mcp-stdio-tools/contracts/tools.md -> "Every success/error tool result"] [Source: .specify/archive/006-mcp-stdio-tools/contracts/store-and-lifecycle.md -> "Drain actual handlers and SDK session"]

## Security

The threat model is the [security review](../../docs/security-review.md) (F01–F16). This section takes up the
storage portions of F03, F05, F09–F13 and F16 as requirements and defers every other finding to a named later
feature. These are requirements, not claims of implemented controls or confirmed vulnerabilities. D1–D18 remain
unchanged.

### Assets and trust boundaries

Local MCP handles stored chats, names, phone/LID mappings and previews. The boundaries are client to invocation, stored WhatsApp-authored content to assistant output, tool work to shared store/ingest resources, and diagnostics to protocol streams. Keep credentials and pairing material outside tool disclosure. Even a trusted client may be prompt-injected. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "Assets are stored conversations"]

[Source: .specify/archive/002-sqlite-storage/spec.md -> "Assets and trust boundaries"]

**Assets**: the linked device's keys and session (whatsmeow device store); message history (text, captions,
senders, times); contacts and the LID-to-phone mapping; media keys, hashes and download URLs; the database's
sidecars, temporary files and backups, which carry the same data.

**Trust boundaries**:

- **WhatsApp content → store.** Messages, names and media metadata originate from anyone who can message the
  account. Ingest (004) passes them in; the store treats them as untrusted data (FR-075, FR-079).
- **Store callers → store.** Ingest and the MCP tools (005–008) call the store directly. Tool arguments come from
  an LLM that can be misled, so the store enforces its own limits independently of the tools (FR-076, FR-077).
- **Filesystem and other OS identities.** Other local users, link substitutions and copied files must not reach
  the database or its sidecars (FR-037, FR-073).
- **Logs and errors → operator and log collectors.** Diagnostics must not become a second copy of secrets or
  conversations (FR-081).
- **Backups and decommissioned copies.** These leave the process's control; the operator owns their protection
  (FR-080, FR-082, FR-083).

Assets are device credentials and identities, shared storage and its sidecars, temporary pairing material, accurate status, protocol stdout, and exclusive ownership until the last device write finishes. Boundaries are owner terminal to pairing control; WhatsApp/network responses to lifecycle and version handling; upstream diagnostics to local logs/terminal; and the connection owner to private shared storage. Neither a retrieved message nor a diagnostic string grants authorization. [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> "Assets are device credentials and identities"]

Assets are the Signal sessions, pre-keys and app-state keys in the device store, the integrity of the server's message queue, the run marker, and the real account's standing with WhatsApp. Boundaries are the client library's background work and the program's storage and network, the old generation and the new one, and the stop diagnostic and the operator's terminal. [Source: .specify/archive/004-shutdown-drain/spec.md -> "Assets and Trust Boundaries"]

**Assets and boundaries**: Messages, captions, names, phone/LID associations and media decryption metadata cross from untrusted WhatsApp senders into private local storage and later consumers. Stored content is data, not instructions. The session and its credentials remain protected by the existing private-storage and shutdown controls. [Source: .specify/archive/005-message-ingest/spec.md -> "Assets and boundaries"]

### Findings taken up or deferred

[Source: .specify/archive/002-sqlite-storage/spec.md -> "Findings taken up or deferred"]

| Finding | In this feature | Deferred to |
| --- | --- | --- |
| F01 Authorization/consent policy | — | 005–007, after the owner's decision is recorded in `docs/specification.md` |
| F02 Remote HTTP/proxy transport | — | 009 |
| F03 Sensitive storage protection | Database, sidecars, temporary files: FR-037, FR-073 | Media downloads 008; secret files 009/010 |
| F04 Secret lifecycle | — | 009, 010 |
| F05 Filesystem containment | Storage paths and objects: FR-073 | Outbox 007; download naming and cache 008 |
| F06 Media processing / ffmpeg | — | 007, 008 |
| F07 Webhook confidentiality/destination | — | 010 |
| F08 Webhook replay/delivery | — | 010 |
| F09 Resource bounds | Store inputs, results, work, waits, disk: FR-076–FR-078, storage acceptance profile | Tool requests 005/006; media 007/008; HTTP sessions 009; webhook queue 010 |
| F10 Input validation | Store inputs and queries: FR-075, FR-079 | Tool argument schemas 005/006; URLs 009/010 |
| F11 Security events in logs | Storage events: FR-081 | Pairing/QR 003; message-body logging 004; tool, send, HTTP and webhook events 005–010 |
| F12 Retention and privacy | Stored history disclosure: FR-082, FR-083 | Media retention 008; webhook recipients 010; overall data-flow inventory 011 |
| F13 Backup/migration recovery | FR-042–FR-044, FR-074, FR-080 | — |
| F14 Incident response | — | 011 (operator runbook) |
| F15 Supply chain | New pure-Go dependencies pass the existing CI module verification and govulncheck | Release provenance and signing 011 |
| F16 Boundary/control ownership | Operator responsibilities for storage: FR-082 | Deployment profiles and control matrix 011, with the owner's decision |

Traceability is limited to this feature: private artifacts/paths address F03/F05 (`AC-3`, `SC-28`); bounded work
and query/input rules address F09/F10 (`SC-5`, `SI-10`); safe events address the storage portion of F11 (`AU-12`);
recovery addresses F13 (`CP-9`, `CP-10`); retention/operator responsibilities address F12/F16 (`SI-12`, `CM-6`).
These mappings do not establish satisfaction of a NIST baseline or close the wider review findings.

The data-directory privacy and lock safety of the [001 hardening](../../docs/backlog/001-binary-skeleton-security-hardening.md)
(`001-SEC-01`, `001-SEC-02`) are merged into `main` (#2) and provide `internal/private`; feature 002 creates its
sensitive files through it and still owns verification of its database, sidecars and temporary artifacts. A check
after config loading or lock acquisition does not retroactively protect earlier operations.

#### Connection and pairing finding dispositions

[Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> "Treatment in this feature and remaining owner"]

| Finding | Treatment in this feature and remaining owner |
| --- | --- |
| F01 - authorization and consent | Local owner pairing window is required by FR-100/FR-101. General read/tool pairing policy belongs to 005-006, send approval requires an owner decision before 007, and transport parity belongs to 009. No MCP pairing capability is enabled here. |
| F02 - remote confidentiality | No HTTP/proxy capability here; defer to 009. Secure version discovery remains required by FR-103. |
| F03 - private storage | Inherit 001/002 controls and verify device-write paths under FR-102. Encryption and backup custody remain operator responsibilities; broader deployment guidance belongs to 011. |
| F04 - secret lifecycle | QR lifetime and credential-free diagnostics are addressed by FR-100/FR-105; HTTP token and webhook key generation/rotation remain in 009/010. |
| F05 - filesystem containment | Device-store paths retain 002's controls under FR-102; outbound paths and cache namespace/cleanup remain in 007/008. |
| F06 - media processing | No media parser/converter enabled; defer to 007/008. |
| F07/F08 - webhook destination/replay | No webhook enabled; defer to 010. |
| F09 - resource exhaustion | FR-103/FR-104 and the bounded profile cover discovery/pairing/retry work. Under constitution 2.0.0 Principles V/X, existing E003-RX-01 in verification.md is an approved temporary deviation only for documented upstream receive-resource gaps: owner tiger-seo, expiry 2026-12-02, compensating controls and earlier-review triggers. The receive bounds remain unmet; device/application-owned controls are not excepted and F09 is not closed. Follow-up: docs/backlog/003-whatsmeow-receive-resource-bounds.md. Ingest bounds belong to 004, tool bounds to 005-006, media bounds to 007-008, transport bounds to 009 and webhook bounds to 010. |
| F10 - input and output validation | FR-103/FR-104/FR-105 cover version, QR and terminal input boundaries; message/tool/media validation remains with 004-008 and HTTP validation with 009. |
| F11 - sensitive logs and audit | FR-100/FR-105 cover lifecycle events and redaction here. Durable audit collection, protected retention and operator review guidance remain in 011; this feature does not claim a complete security audit trail. |
| F12 - retention/privacy | No QR persistence or transcript evidence under FR-100. Message/client data flows remain with 004-006; media/webhook flows with 008/010; full retention and decommissioning guidance with 011, extending existing storage operations guidance. |
| F13 - recovery and backups | Retain 002's storage recovery controls; FR-096 prevents active device writes during close. Synthetic tests only for restored device state; broader operator recovery rehearsal remains in 011. |
| F14 - incident response | Revoked credentials cannot be reused by FR-088. Full compromise/rotation/unlink runbook belongs to 011 with credential-specific steps in 009/010; this feature does not auto-unlink a phone or destroy data. |
| F15 - supply chain | FR-107 applies now; release signatures, provenance and distribution guidance remain in 011. |
| F16 - assurance/ownership | Record feature-specific security evidence and limitations; deployment/control ownership and any regulated cryptographic assurance profile remain operator decisions in 011. No compliance claim is made. |

#### Quiet-stop finding dispositions

| Finding | Treatment in this feature |
| --- | --- |
| F09 - resource exhaustion | The observation is bounded in memory and time (FR-115). No new connection path: the post-pair connection already existed. But a reusable session makes repeated, long-running connected `stdio` and `serve` runs possible for the first time, which increases receive work in practice. E003-RX-01 asks for a recheck "before another feature materially expands the receive workload or claims this exception", so the owner decides, before implementation starts, whether E003-RX-01 covers this feature, because it is not inherited by another feature (constitution X) and this feature makes long connected runs possible. The facts (scope, pin, expiry at the start of 2026-12-02 UTC, no renewal) and the owner's answer are recorded in `verification.md` at implementation readiness and again at code review. Its full reassessment belongs with stage 2 or the expiry, whichever comes first. If the exception expires before this feature's code review, the work stops until the owner records a new decision.  [Source: .specify/archive/004-shutdown-drain/spec.md -> "F09 - resource exhaustion"] |
| F11 - sensitive logs | Only the outcome, category, counts and duration are reported (FR-115, FR-116).  [Source: .specify/archive/004-shutdown-drain/spec.md -> "F11 - sensitive logs"] |
| F13 - recovery | Offline recovery is unchanged and remains the fallback for every failed check.  [Source: .specify/archive/004-shutdown-drain/spec.md -> "F13 - recovery"] |
| F14 - incident response | A logout latch still needs durable cleanup before the marker clears (FR-110).  [Source: .specify/archive/004-shutdown-drain/spec.md -> "F14 - incident response"] |
| F15 - supply chain | No fork, no `replace` and no new dependency (FR-125). The pin moved once, to upstream `f1b7985`, by owner decision with an E003-RX-01 reassessment; the 20 upstream commits were read for reconnect and storage changes, and the new automatic-reconnect paths stay off. The allow-list is revalidated on any version change (FR-117); it is empty.  [Source: .specify/archive/004-shutdown-drain/spec.md -> "F15 - supply chain"] |
| Others | Not touched by this feature; dispositions in feature 003 stand.  [Source: .specify/archive/004-shutdown-drain/spec.md -> "Others"] |

#### Ingest finding dispositions

- **F03, F05, F13**: Preserve feature 002's private storage, safe paths, atomic writes and recovery guarantees. Media is not downloaded or opened here; download path containment is feature 009. [Source: .specify/archive/005-message-ingest/spec.md -> "**F03, F05, F13**"]

- **F09**: Enforce actual application-owned field, byte, batch, concurrency, wait and disk limits, including for history, mapping and cached names; reject excess safely, without unlimited queues or silent success. Planning must identify measurable limits and failure behavior before implementation. Device-store limits remain mandatory. E003-RX-01 is limited to its explicitly approved upstream receive paths and does not automatically cover 005. [Source: .specify/archive/005-message-ingest/spec.md -> "**F09**"]

- **F10**: Validate untrusted identities, timestamps and field sizes before storage; preserve valid content as data and safely encode untrusted names in diagnostics. A rejected record causes no partial message write. [Source: .specify/archive/005-message-ingest/spec.md -> "**F10**"]

- **F11**: Message bodies and captions MUST NOT appear above debug level; credentials, media keys and QR material MUST NOT appear at any log level. Failure diagnostics MUST avoid unnecessary personal data and safely encode control characters. Broader audit policy is deferred to feature 012. [Source: .specify/archive/005-message-ingest/spec.md -> "**F11**"]

- **F12**: Extend operator-facing privacy and retention guidance to identify stored message content, media metadata and LID-phone linkage, indefinite growth and the limits of deletion and backup protection; downstream export belongs to features 006-007 and 011. [Source: .specify/archive/005-message-ingest/spec.md -> "**F12**"]

- **F01**: Ingest never interprets stored text as instructions or authorizes a send, pairing or other account action. Read/send authorization policy is a separate owner decision for the affected tool features. [Source: .specify/archive/005-message-ingest/spec.md -> "**F01**"]

- **F02, F04, F06-F08, F14-F16**: Remote transport and secret lifecycle belong to feature 010; media processing to 008-009; webhook destination and delivery to 011; incident response, release provenance and deployment assurance to 012. Existing controls and dependency checks remain in force. [Source: .specify/archive/005-message-ingest/spec.md -> "**F02, F04, F06-F08, F14-F16**"]

#### Local stdio finding dispositions

| Finding | Treatment and remaining scope |
| --- | --- |
| F01 | Owner decision of 2026-11-17 allows all local chat/contact reads without per-read prompts; prompt-injection risk remains. Configurable read restrictions and MCP login/QR are deferred; send authorization belongs to 008, transport parity to 010. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "F01 - authorization/consent"] |
| F02 | No remote listener/proxy; protection belongs to 010. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "F02 - remote confidentiality"] |
| F03 | Inherit private storage; at-rest/backup encryption and downstream custody remain operator responsibilities; guidance in 012. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "F03 - private storage"] |
| F04 | No credential/QR disclosure; MCP QR lifecycle deferred; HTTP/HMAC secrets in 010/011. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "F04 - secret lifecycle"] |
| F05/F06 | No outbound file/media operations; inherit directory checks. Send/conversion in 008, download/cache in 009. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "F05/F06 - paths and media"] |
| F07/F08 | No webhook; destination, replay and delivery controls in 011. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "F07/F08 - webhooks"] |
| F09 | Bound stdio, query and output work and protect ingest admission; review concrete bounds before implementation. No inherited receive exception; later message/media/HTTP bounds belong to 007–010. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "F09 - resource exhaustion"] |
| F10 | Validate schema/handler parity, normalization, filtering, pagination and rendering. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "F10 - validation"] |
| F11 | Redact payloads/QR and report bounded outcomes; durable audit collection, retention and review in 012. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "F11 - logs/audit"] |
| F12 | Client/provider receives selected data and linkage; owner must understand recipient retention. No new persistence; full inventory in 012. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "F12 - retention/privacy"] |
| F13 | Preserve storage and drain controls; no tool restoration. Rehearsal and guidance in 012. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "F13 - recovery/backups"] |
| F14 | No automatic unlink, credential deletion or recovery. Keep fail-closed diagnostics; runbook in 012 and credential rotation in 010/011. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "F14 - incident response"] |
| F15 | Exact dependency/integrity/vulnerability gates now; release signing, provenance and identity in 012. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "F15 - supply chain"] |
| F16 | Local single-owner profile, synthetic evidence with platform limits; deployment and regulated assurance decisions in 012. No compliance claim. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "F16 - assurance/ownership"] |

### Security requirements

- **FR-153**: Enforce owner-approved local read scope before disclosure. No tool argument, stored content or client approval claim authorizes pair, recovery or send. No restricted-read profile; prompt-injection disclosure risk remains. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> SEC-001]
- **FR-154**: Expose no QR retrieval or pairing-window control; exclude pairing material and device credentials from tool output, logs, errors and evidence. Keep terminal pairing safeguards. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> SEC-002]
- **FR-155**: Treat all returned names, identities, captions and text as untrusted data with clear provenance. Escape controls safely in text/diagnostics without changing authoritative values. Exclude decryption metadata and device-store secrets; never log successful payloads or QR results. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> SEC-003]
- **FR-156**: Validate lengths, types, identity domains, literal queries, sort choices and pagination before expensive work. Reject malformed, deep and oversized input. Bound actual input, processing, query results, serialization and emitted bytes; repeated calls and slow output cannot create unbounded work. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> SEC-004]
- **FR-157**: Preserve private storage on Unix and Windows and the safe shutdown/recovery gates. Tool errors cannot initiate repair, deletion, marker clearing, new account generations, result-file writes or automatic recovery. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> SEC-005]
- **FR-158**: Mandatorily report denied/failed requests, terminal faults, incomplete history and receive loss with bounded secret-free tool/component, time, outcome and category. Failed admission or acknowledgment by deadline latches failure and ends stdio unsuccessfully through normal cleanup; never claim delivery. Successful-read/debug events are best effort without queries, names, numbers or bodies. Keep diagnostics on stderr and existing debug-only body restrictions; no new body logging. Durable audit retention is release work, not proved by OS acknowledgment. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> SEC-006]
- **FR-159**: Retain exact dependency versions, module integrity, reachable-vulnerability checks and five CGO-free builds; the SDK does not authorize a whatsmeow bump. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> SEC-007]

- **FR-073**: Filesystem checks MUST protect the actual objects opened/created, not only a pathname checked earlier. [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-040]
  The data-directory path and database/sidecar/temp entries MUST resist replacement by unrelated identities;
  unsupported symlinks, junctions/reparse points, hard-linked database/sidecar files, non-regular files and unsafe
  path aliases MUST be rejected before mutation. The platform contract MUST state supported local filesystems and
  handling of UNC/device paths and alternate data streams; unsupported forms fail closed. A path form is judged by
  where it resolves, so a mapped network drive or a `subst` drive that leads to a network path is rejected like a
  UNC path. SQLite-created artifacts MUST stay in protected locations. Rechecking names alone is not sufficient for
  a check/open race: the private data directory keeps unrelated identities from replacing entries, and after
  SQLite opens the database the store MUST confirm that the file at each path is the same file it checked (by file
  identity) and fail startup on a mismatch. Substitution by the same OS identity is outside this feature's threat
  model (see "Private storage is not encryption").
- **FR-074**: Startup MUST check both schema histories, upgrade the device schema, then apply message migrations, [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-041]
  stopping at the first error and closing all opened resources. Each device-upgrade step and its version record
  MUST likewise have a verified atomic failure boundary; if the pinned upstream mechanism cannot provide it,
  resolve that incompatibility before implementation rather than promising unsupported rollback or hand-editing
  its schema. Earlier successful steps remain committed, no whole-startup rollback is promised, and the safe error
  MUST identify completed/failed steps. Retry MUST resume from committed versions without downgrade or automatic
  restore. A failed or indeterminate upgrade MUST never yield a partially usable store.
- **FR-075**: Store operations MUST validate types, required fields, syntax and approved byte/range limits before [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-042]
  issuing mutations. Chat JIDs use `s.whatsapp.net`, `g.us` or `lid`; sender JIDs use `s.whatsapp.net` or `lid`, with
  device components normalised as in story 3. Empty/malformed identifiers and unsupported domains are rejected;
  phone numbers follow FR-068. Message IDs are opaque values, never paths or SQL. Media types are empty or the
  supported image/video/audio/document types; present keys/hashes, lengths, URLs and MIME metadata MUST meet the
  approved storage contract, including nonnegative, representable file lengths. Missing optional media metadata
  may be stored for later download validation; metadata is never fetched or executed by the store. Required message
  times and supplied filters MUST fit the documented timestamp range; absent chat activity remains allowed, and
  reversed filters retain the empty-result behavior above. Original message/name text is preserved, not executed
  or silently sanitised; diagnostics encode it safely under FR-081.
- **FR-076**: All direct store calls MUST enforce the approved storage acceptance profile: field/search byte lengths, [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-043]
  batch count/aggregate bytes, list size/offset, per-side and total expanded context, aggregate result bytes
  (including ambiguous-ID results), active/waiting work, database connections, SQLite's own memory and
  wait/operation deadlines. Base
  list sizes MUST NOT exceed 200; tools still own their existing defaults and page-to-offset conversion (D9).
  Context rows have a separate aggregate bound, not an implicit exemption. Arithmetic for offsets, sizes and
  expansion MUST be checked before SQL/allocation. Reject oversized requests before expensive work where possible;
  bound materialisation of existing/upstream data too, without silent truncation or partial successful results.
  Excess work returns distinct validation/resource-limit/busy errors. No unbounded queue or retry loop is allowed.
- **FR-077**: Every blocking operation MUST accept cancellation and use the earlier of its caller deadline and its [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-044]
  configured operation budget, including admission, SQL, lock waits and iteration. The configured busy timeout
  remains at least 5 seconds (FR-035), but MUST NOT override an earlier deadline/cancellation. Transactions MUST
  release resources on cancellation/failure and report confirmed versus indeterminate commit outcomes honestly.
  Startup upgrades and maintenance have separately approved finite budgets rather than unlimited waits.
- **FR-078**: Disk-low admission and disk-full/write errors MUST preserve committed state and fail without automatic [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-045]
  deletion of device keys, messages, journals or backups. The acceptance profile MUST define the free-space reserve,
  measurement interval and safe write-resumption rule. Already admitted/device-store writes may still encounter
  full storage and MUST fail safely; a free-space check is not a guarantee of available disk. Bounded WAL/checkpoint
  behavior and long-reader handling MUST be documented and tested.
- **FR-079**: All SQL values, including search patterns, identifiers used as values and pagination, MUST be bound [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-046]
  parameters. Any dynamic SQL identifiers, sort columns or directions MUST come only from fixed allow-lists;
  unsupported sort values are errors. Literal wildcard escaping and Unicode matching (FR-067) remain required;
  injection-like but valid search strings MUST NOT be rejected merely to avoid safe parameterization.
- **FR-080**: Feature acceptance MUST include an operator guide: a "Storage" section in a tracked `OPERATIONS.md` at [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-047]
  the repository root, which later features extend and the README (011) links to. It MUST give a procedure for a
  coherent, quiesced SQLite backup: stop the
  owner and all connections, verify successful checkpoint/clean closure, and protect the resulting backup like
  live credentials. Copying only the main file while writes/WAL are active is explicitly unsupported. If clean
  checkpoint/closure cannot be verified, preserve the coherent stopped file set for documented recovery rather
  than discarding sidecars. Restore into a protected, isolated directory, offline, and validate SQLite integrity,
  foreign keys, both schema versions and expected data before replacing live storage. Keep the original protected
  until validation succeeds. An online method may be documented only if actually supported and tested. The guide
  MUST warn that directory locks do not coordinate copies and that a restored device store MUST NOT connect beside
  its original. Live account checks/re-pairing require separate explicit permission; no new backup command is added.
- **FR-081**: Storage startup/shutdown, permission/path refusal, schema preflight/upgrade success/failure and storage [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-048]
  resource/I/O failures MUST produce structured events with UTC time, operation/correlation, schema/version where
  relevant, outcome and a safe reason. Logs/errors MUST exclude device/media keys, tokens, QR data, sensitive SQL
  parameters and message/caption bodies at every level in this feature. Do not forward raw driver/upstream errors
  that can contain these values. Paths and untrusted diagnostic fields MUST be bounded and control-character-safe.
  Retain useful safe operation/version/error codes and stderr in stdio mode; no new audit service is required.
- **FR-082**: The operator guide MUST disclose database, [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-049]
  WAL and backup growth; data classes include device credentials, contacts/LID mappings, text/captions and media
  keys. The operator selects protected backup destinations, retention, recovery-point/recovery-time objectives,
  disk/backup encryption and restore rehearsals. Owner-only access is not encryption at rest, and this feature
  neither adds SQLite encryption nor establishes a NIST/FIPS assurance claim.
- **FR-083**: The operator guide MUST document offline decommissioning: stop the owner, inventory only this installation's database, [Source: .specify/archive/002-sqlite-storage/spec.md -> FR-050]
  sidecars, temporary artifacts and related caches/logs/backups, and explicitly identify responsibility for copies
  held elsewhere. Explain that unlinking files does not guarantee erasure on SSDs, snapshots or backups, and that
  encryption/key destruction depends on the operator's storage system. Do not delete unrelated files or add
  WhatsApp-message deletion/automatic retention as part of this feature.

- **FR-100**: QR material MUST remain transient and available only in the active owner-approved attempt. Each code MUST be cleared on its expiry or replacement; all pairing material MUST be cleared on the attempt's final timeout, cancellation, success or logout. QR payloads and session secrets MUST NOT appear in logs, errors, ordinary status output, temporary files or verification evidence, including at debug level. Deliberate terminal QR display is the sole human-display exception in this feature.
  [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> SEC-001]

- **FR-101**: Pairing MUST only begin on explicit owner request, not on status reads, startup without credentials, network disconnection or unsolicited events. This feature MUST NOT expose an unauthenticated listener or a remotely callable pairing endpoint.
  [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> SEC-002]

- **FR-102**: Existing private-file and handle-identity protections MUST remain effective for client-created device data and sidecars. Unsafe storage MUST fail before account connection or pairing. No revoked-session cleanup may delete chats, messages or unrelated files. Private storage is not encryption; host and backup encryption remain operator responsibilities.
  [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> SEC-003]

- **FR-103**: Version discovery MUST validate a secure upstream response, reject invalid certificates and unexpected redirects, consume bounded time and bytes, and accept only a well-formed supported version. Rejected discovery MUST preserve the prior version, expose no credentials, and never redirect account credentials to another service.
  [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> SEC-004]

- **FR-104**: Concurrent lifecycle work and externally supplied QR/version data MUST be bounded by the acceptance profile below. Cancellation MUST prevent delayed work from starting another connection. Over-limit input MUST be rejected without changing saved session state or replacing a valid current version/QR.
  [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> SEC-005]

- **FR-105**: Upstream text MUST be safely encoded for logs and terminal diagnostics, with no attacker-controlled terminal escapes. Logs MUST record pairing requested/succeeded/expired/failed, logout, version refresh failure, retry exhaustion and shutdown failure using time, outcome and a non-secret reason, without raw QR data, keys or unnecessary account identifiers.
  [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> SEC-006]

- **FR-106**: Automated tests MUST use synthetic credentials, simulated events and controlled version responses, never real account data or live account connections. Real-account verification MUST obtain the user's explicit permission separately for each account-affecting step and record changes and cleanup without secrets.
  [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> SEC-007]
  Automated tests use the simulator and synthetic data only. Each real-account step needs the owner's separate permission and is recorded with redacted results. [Source: .specify/archive/004-shutdown-drain/spec.md -> SEC-006]

- **FR-107**: Dependencies MUST preserve the existing exact-version, module-integrity, reachable-vulnerability and five-platform build rules. No connection or QR dependency may require a C toolchain.
  [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> SEC-008]

- **FR-120**: The check fails closed in every case of doubt (FR-109). A false pass is the main new risk: it would let a later run start while lost writes leave sessions, pre-keys or app-state out of step. [Source: .specify/archive/004-shutdown-drain/spec.md -> SEC-001]

- **FR-121**: Network before storage, and the library context cancelled only after both blocks, are invariants with their own tests (FR-108, SC-031). [Source: .specify/archive/004-shutdown-drain/spec.md -> SEC-002]

- **FR-122**: No traffic from a retired generation reaches another generation's connection (FR-111, FR-114, SC-030). [Source: .specify/archive/004-shutdown-drain/spec.md -> SEC-003]

- **FR-123**: Observation data never leaves the check (FR-115). It can hold raw memory words. [Source: .specify/archive/004-shutdown-drain/spec.md -> SEC-004]

- **FR-124**: No new unattended network activity. The only reconnect is the existing post-pair one in `pair`, which the owner starts (FR-112, FR-113). [Source: .specify/archive/004-shutdown-drain/spec.md -> SEC-005]

- **FR-125**: No new dependency, no fork and no `replace`. The pinned client library version changes only by an owner decision with its own E003-RX-01 reassessment. The one change made is the 2026-11-09 move to upstream `f1b7985` (`v0.0.0-20261109152745-f1b798561919`), which fixes a socket read loop that could block forever after a stop. No other module changed. [Source: .specify/archive/004-shutdown-drain/spec.md -> SEC-007]

### Storage acceptance profile (approved 2026-10-28)

[Source: .specify/archive/002-sqlite-storage/spec.md -> "Storage acceptance profile (approved 2026-10-28)"]

Approved by the owner on 2026-10-28. The values are fixed constants in code, not configuration settings, and do
not change tool defaults. Every value is enforced on what is actually consumed and has a boundary test and an
excess/overflow test (SC-016). Zero or unset never means unlimited. Sizes are UTF-8 bytes; KiB/MiB/GiB are powers
of 1024.

| Parameter group | Approved values |
| --- | --- |
| Fields and metadata | JID ≤ 256 B; message ID ≤ 256 B; chat and contact names ≤ 1 KiB; text ≤ 256 KiB; caption ≤ 256 KiB; search text ≤ 1 KiB; URL ≤ 4 KiB; direct path ≤ 4 KiB; MIME type ≤ 255 B; original file name ≤ 1 KiB; media key, file hash and encrypted file hash exactly 32 B when present; file length 0 to 4 GiB; message times and time filters from 2000-01-01T00:00:00Z (inclusive) to 2100-01-01T00:00:00Z (exclusive). A value outside these is a validation error naming the field, with nothing stored |
| Batch and results | Batch ≤ 1,000 messages and ≤ 16 MiB aggregate field bytes; chat-update batch ≤ 1,000 updates (their size is bounded by the field limits); list size 1–200; offset 0–100,000; context ≤ 20 rows per side and ≤ 2,000 context rows per call in total; ≤ 32 MiB of result bytes per call; an ambiguous-ID error lists ≤ 100 chats. Existing or upstream data that would exceed a result bound returns a resource-limit error, never a truncated or partial result |
| Work and waits | 1 write connection and 4 read connections; ≤ 16 active and ≤ 64 waiting operations in this storage baseline (beyond that: immediate busy error); admission wait ≤ 5 s; busy timeout 5 s; each query or write ≤ 10 s; resources released within 1 s of cancellation in the storage acceptance profile; startup schema upgrades ≤ 10 min in total. The shutdown timer reports failure but does not authorize early close or unlock (FR-029). SQLite memory (page cache, sorts, temporary b-trees) ≤ 256 MiB per process as a hard heap limit, beyond which the operation fails with a resource-limit error (approved 2026-10-28, second design review). The bound of 2 deep sender listings at once (third design review) was withdrawn on 2026-10-30: sender listings no longer sort, so their memory does not grow with the offset |
| Disk and WAL | Free-space reserve 256 MiB, checked before each write transaction from a measurement at most 30 s old; once refused, writes resume when ≥ 512 MiB is free. The write-ahead log is truncated to ≤ 64 MiB after checkpoints; a long reader is bounded by the 10 s query deadline. Nothing (keys, messages, journals, backups) is deleted automatically |
| Platform and recovery | Local filesystems only (NTFS on Windows; ext4 and other local POSIX filesystems; APFS). UNC/network paths (including mapped and `subst` drive letters that resolve to one), `\\?\` and `\\.\` device paths and alternate data streams are rejected. On Windows the access list is limited to the user, SYSTEM and Administrators (constitution Principle V). Upstream device-migration guarantees, backup/restore steps and the isolated cross-identity test procedure are verified and documented under FR-044, FR-074, FR-080 and FR-072 |

### Connection acceptance profile

[Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> "These owner-approved fixed limits concern application-owned discovery and lifecycle work"]

These owner-approved fixed limits concern application-owned discovery and lifecycle work; they do not claim to bound all resources internal to the WhatsApp service or its protocol library. Planning must enumerate the library's relevant limits and any remaining exposure before implementation is approved. Changes to this profile require explicit review rather than silent adjustment during implementation.

| Resource | Required bound and failure behaviour |
| --- | --- |
| Active client and pairing | One owned client and one active pairing attempt per process; concurrent QR requests reuse that attempt rather than queueing new attempts. |
| Application-initiated connection attempts | One in flight; each owner-initiated initial, explicit, startup-retry, 405 or 515 connection attempt has a 30-second watchdog, cancellable by shutdown. On timeout cancel and drain before another attempt; an undrained attempt retains ownership. Ordinary library recovery must not spin on immediate failures or bypass terminal logout/405 failure. |
| Saved-session shell startup recovery | After an initial failure or timeout, at most three retries, each after a cancellable five-second wait; on exhaustion remain disconnected with an actionable diagnostic. This budget is separate from and cannot bypass 405 recovery, logout or shutdown. |
| Version discovery | One request in flight; 15-second overall deadline; at most 1 MiB of actual response-body consumption; unexpected redirects rejected. |
| QR payload | At most 4 KiB per current code; retain at most one code and one derived display/image representation; no history of codes. Validity ends at upstream expiry or after 60 seconds from receipt, whichever occurs first. |
| Automatic outdated recovery | Three retries after the initial rejection, five seconds before each, reset only by confirmed connection. |
| Version scheduling | One six-hour refresh schedule; overlapping refresh requests share or skip the in-flight request rather than accumulating work. |
| Stop | Cancel outstanding work immediately; report failure if quiescence is not achieved within 10 seconds. The process remains alive holding the store and lock until writes are quiescent, then closes and exits unsuccessfully. A forced process kill remains an OS/crash boundary, not clean shutdown. |

### Abuse cases

[Source: .specify/archive/002-sqlite-storage/spec.md -> "Abuse cases"]

Each is a negative test with synthetic data. It shows the rejected action has no side effect: nothing is written,
created, repaired or deleted, and a sentinel file outside the data directory stays unchanged. A platform-specific
case is tested on each platform it covers; one that cannot be run there is recorded as not verified.

1. An unrelated OS identity tries to read or write the database, WAL/SHM or a temporary file (Unix and Windows):
   denied.
2. The database or a sidecar is a symlink, junction/reparse point, hard link or non-regular file: startup refuses
   before any write, and the link target is untouched. The database file is replaced between the check and
   SQLite's open (a test hook stands in for the attacker): the store detects that the opened path no longer holds
   the checked file, closes without migrating or storing anything, and fails startup. An unrelated identity cannot
   make that replacement at all, because it cannot create or rename entries in the private data directory (shown
   in the cross-identity evidence).
3. A preexisting data directory, database or sidecar is readable by others: startup refuses with the fix, and the
   permissions are not changed.
4. Search text contains SQL fragments, quotes, `%`, `_` or a backslash: it matches literally and returns only
   literal matches.
5. An unsupported sort value, a malformed JID or message ID, malformed media metadata, an oversized field, or an
   overflowing offset or context count is passed: validation error, nothing stored.
6. An oversized history batch is stored: rejected as a whole, with no prefix stored.
7. Calls exceed 16 active and 64 waiting operations: the excess gets a busy error at once; memory and goroutines
   stay bounded.
8. Context expansion or results exceed the row or byte limits, or a query needs more than SQLite's memory limit:
   resource-limit error, not a truncated result or a crash.
9. Free space falls below the reserve, or the disk is full: writes are refused, and no key, message, journal or
   backup is deleted.
10. A database with a newer schema version is opened: refused, and the main file and sidecars are byte-for-byte
    unchanged, with none created or removed.
11. Success and failure paths run over seeded device keys, media keys, message bodies and names containing
    terminal control sequences: none of them appear in logs or errors at any level.
12. A restored backup is validated offline only: neither it nor the original device store connects to WhatsApp.

#### Connection and pairing abuse cases

- Flood concurrent pairing and 405 events: verify one active attempt, no multiplied retry budget and no unexpected session writes. [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> "Flood concurrent pairing and 405 events: verify one active attempt, no"]

- Replay expired QR/success/logout events from an older attempt: verify they cannot reveal stale material or mutate the current attempt's saved state. [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> "Replay expired QR/success/logout events from an older attempt: verify they"]

- Submit oversized, malformed, stalled or redirected version responses and malformed/oversized QR payloads: verify deadlines and consumption bounds, unchanged valid state and no persisted rejected input. [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> "Submit oversized, malformed, stalled or redirected version responses and"]

- Inject terminal controls and synthetic secrets into upstream error paths: capture every log level and status output; verify safe rendering and no credential or QR leakage. [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> "Inject terminal controls and synthetic secrets into upstream error paths:"]

- Point the process at unsafe or substituted device storage: verify refusal before any account network operation and no application writes to the rejected target. Exercise Windows and Unix protections separately. [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> "Point the process at unsafe or substituted device storage: verify refusal"]

- Interrupt pairing, retry sleep, discovery and an in-flight device write: verify no post-cancellation reconnect, no database close before writes finish, and no early lock release. [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> "Interrupt pairing, retry sleep, discovery and an in-flight device write:"]

- Run standalone status with network access prohibited, missing storage and another active owner: verify no connection, QR creation, migration or saved-state mutation and an honest snapshot/error. [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> "Run standalone status with network access prohibited, missing storage and"]

The feature's later `verification.md` must record synthetic test evidence, supported-platform coverage, measured bounds, explicitly authorized manual results and any unverified limitations. Specifying a requirement is not evidence that its control has passed. [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> "The feature's later verification.md must record synthetic test evidence"]

#### Quiet-stop abuse cases and evidence

- Leave a library task running past the deadline: the marker stays unsafe and nothing is written after the storage block. [Source: .specify/archive/004-shutdown-drain/spec.md -> "Leave a library task running past the deadline"]
- Send from the pairing generation after its network block: the send is refused and never seen by the fresh connection. A new dial is counted and fails the check; a write on a connection the block already closed sends nothing and is counted, but does not fail it. [Source: .specify/archive/004-shutdown-drain/spec.md -> "Send from the pairing generation after its network block"]
- Write to storage after its block: the write is refused, counted, the check fails and no row changes. [Source: .specify/archive/004-shutdown-drain/spec.md -> "Write to storage after its block"]
- Read storage after its block, as the library's queued app-state fetches do when the stop lands during a sync: the read is refused and counted separately, nothing changes, and the check does not fail on it. [Source: .specify/archive/004-shutdown-drain/spec.md -> "Read storage after its block, as the library's queued app-state fetches do when the stop lands during a sync"]
- Cut the observation off at its size bound: the check fails. [Source: .specify/archive/004-shutdown-drain/spec.md -> "Cut the observation off at its size bound"]
- Deliver a logout during the check, and a stop signal during the check: the marker stays unsafe unless the logout cleanup is durable, and the drain order holds. [Source: .specify/archive/004-shutdown-drain/spec.md -> "Deliver a logout during the check, and a stop signal during the check"]
- Replay feature 003's late-logout ordering (`TestRunGuardLatePinnedLogoutAfterRetirement`) and a held client-library message handler across retirement, which feature 003 left unproven: the check fails and no clean marker is committed while either is pending. [Source: .specify/archive/004-shutdown-drain/spec.md -> "Replay feature 003's late-logout ordering (`TestRunGuardLatePinnedLogoutAfterRetirement`) and a held client-library message handler across retirement, which feature 003 left unproven"]
- Make the pairing generation's error tail or QR emitter call disconnect after the fresh connection starts: the call reaches only the retired client and the fresh connection stays up. [Source: .specify/archive/004-shutdown-drain/spec.md -> "Make the pairing generation's error tail or QR emitter call disconnect after the fresh connection starts"]
- Refuse the marker clear for low disk: the marker stays unsafe and the operator is told what to do. [Source: .specify/archive/004-shutdown-drain/spec.md -> "Refuse the marker clear for low disk"]
- Capture every log level during each case: no raw observation data, keys or account identifiers. [Source: .specify/archive/004-shutdown-drain/spec.md -> "Capture every log level during each case"]

`verification.md` records the simulator results, the measured pass rate, the platforms covered and any permitted real-account results. Anything not run is reported as not verified. [Source: .specify/archive/004-shutdown-drain/spec.md -> "Abuse Cases and Verification"]

#### Ingest abuse cases and evidence

Synthetic tests cover oversized text, captions, names, metadata and history; malformed JIDs, timestamps and control characters; cache and concurrent-event pressure; mapping and storage failures; disk exhaustion; malicious message instructions; and callbacks racing retirement. Rejected inputs must not write a partial message or trigger a send, pairing, webhook or other external side effect. Platform-specific claims require platform-specific evidence. [Source: .specify/archive/005-message-ingest/spec.md -> "Abuse cases"]

#### Local stdio abuse cases and evidence

- Reject oversized/deep or ill-typed JSON, unknown fields, query-like text, malformed identities and overflow pagination within bounds, without account or conversation changes. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "Submit oversized/deep JSON"]
- Flood reads during ingest, cancel queued/active calls and stall the reader; measure limits, continued ingest and actual drain. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "Flood chat/contact reads"]
- Use synthetic names/previews with terminal controls, fake approvals and secrets; prove safe rendering, provenance, no account action and no diagnostic leakage. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "Seed names and previews"]
- Call deferred login/QR tools and submit fake approval claims; prove unknown-tool failure without disclosure or side effects. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "Attempt to invoke deferred login/QR tools"]
- Trigger device/ingest faults and disconnect/EOF during calls; prove failure exit, ordered drain, no early store close/unlock and no invalid marker clear. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "Trigger device-storage/ingest terminal faults"]
- Capture streams during initialization, tool success/failure, lock refusal and shutdown across log levels. Record commands, dates, counts, exits, measured bounds and platform limits in verification; mark unrun controls as not verified. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "Capture stdout/stderr"] [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "The later `verification.md` must record"]

## Success Criteria

### Measurable Outcomes

- **SC-001**: `whatsapp-mcp version` works on all 5 targets and returns within 1 second, with or without a valid [Source: .specify/archive/001-binary-skeleton/spec.md -> SC-001]
  configuration.
- **SC-002**: For every pair of configuration sources, automated tests show the higher source winning; for every [Source: .specify/archive/001-binary-skeleton/spec.md -> SC-002]
  setting, at least one invalid value in each source it can come from is rejected at startup with a message that
  names the setting and the source.
- **SC-003**: A second instance on the same data directory fails within 1 second with a message that contains the [Source: .specify/archive/001-binary-skeleton/spec.md -> SC-003]
  first instance's PID; after the first instance is force-killed, a new instance starts at the first attempt.
- **SC-004**: In the clean-stop case, the command exits 0 within the ten-second bound and the data directory
  can be locked again at once. In a held-work case, the timeout is reported while storage and the lock are
  retained; when the work is released, ordered cleanup finishes and the command exits nonzero. No second signal
  is sent in either case. [Source: .specify/archive/001-binary-skeleton/spec.md -> SC-004] [Source: .specify/archive/003-whatsapp-pairing-status/contracts/connection-owner.md -> "Stop and Failure Contract"]
- **SC-006**: Runs from at least 2 different working directories without an override use the same data directory. [Source: .specify/archive/001-binary-skeleton/spec.md -> SC-006]
- **SC-007**: CI is green on the feature's pull request for formatting, static checks, tests on 3 operating [Source: .specify/archive/001-binary-skeleton/spec.md -> SC-007]
  systems and builds for all 5 targets; a deliberately unformatted file makes it fail.

- **SC-008**: After a first start on an empty data directory, the data directory holds exactly 1 database file [Source: .specify/archive/002-sqlite-storage/spec.md -> SC-001]
  (plus its journal files), containing both the device store and the message tables; a restart changes no schema and
  keeps 100% of stored chats and messages.
- **SC-009**: The store's automated tests (FR-072) pass on Linux, Windows and macOS in CI, using only a temporary [Source: .specify/archive/002-sqlite-storage/spec.md -> SC-002]
  directory and no network.
- **SC-010**: Storing the same set of chats and messages in at least 20 different random orders, with duplicates [Source: .specify/archive/002-sqlite-storage/spec.md -> SC-003]
  of identical content, produces identical chats (names and last activity times) and identical messages every
  time. The one exception is a message re-stored under the same `(chat_jid, id)` with different content: there the
   copy written last is kept (FR-048), and a separate test checks that rule.
  For all tested arrival orders of distinct messages, chat last activity equals the newest stored message time; duplicate copies follow the stated replacement rule. [Source: .specify/archive/005-message-ingest/spec.md -> SC-003]
- **SC-011**: On a database of 100,000 messages across 1,000 chats, every query in FR-054–FR-066 returns within 200 ms. [Source: .specify/archive/002-sqlite-storage/spec.md -> SC-004]
  This is measured by a Go benchmark over a seeded database of that size, run on the maintainer's machine. The
  results and the machine (CPU, memory, disk, OS) are recorded in the feature's `verification.md`. It is not a
  CI gate.
- **SC-012**: 16 concurrent writers making 100 writes each, while queries run, complete with 0 "database is locked" [Source: .specify/archive/002-sqlite-storage/spec.md -> SC-005]
  errors.
- **SC-013**: Opening a database marked with a newer message-schema version fails every time, and the file is [Source: .specify/archive/002-sqlite-storage/spec.md -> SC-006]
  byte-for-byte unchanged afterwards, as do existing sidecars, with none created or removed. The same holds for a
  newer device schema and the other FR-043 preflight refusals. A failing migration instead leaves logical schema,
  version and data unchanged at that step's boundary; combined failures obey FR-074, not whole-startup rollback.
- **SC-014**: After the process is killed during a stream of writes, the next start opens the database without error [Source: .specify/archive/002-sqlite-storage/spec.md -> SC-007]
  and every write that had committed is present.
- **SC-015**: Unix and Windows evidence demonstrates that unrelated identities cannot read/write any sensitive [Source: .specify/archive/002-sqlite-storage/spec.md -> SC-008]
  artifact in FR-037. Every tested unsafe preexisting artifact/path substitution fails before mutation, including
  WAL/SHM and temporary-file cases; unsupported/unverified platform guarantees are not counted as passed.
- **SC-016**: For every approved profile parameter, tests cover its boundary and excess/overflow cases, record [Source: .specify/archive/002-sqlite-storage/spec.md -> SC-009]
  elapsed time, memory/result bytes, active/waiting work and disk/WAL growth as applicable, and verify the approved
  ceilings and cancellation allowance. SC-011/SC-012 remain normal-load targets, not substitutes for these bounds.
- **SC-017**: An offline restore of synthetic device/message data backed up after a quiesced write workload passes [Source: .specify/archive/002-sqlite-storage/spec.md -> SC-010]
  integrity/foreign-key checks, preserves both schema histories and every pre-quiescence committed write, and
  satisfies private-access rules. Interrupted upgrades and disk-full failures follow the documented recovery path.
- **SC-018**: Captured startup, success and failure diagnostics at every log level contain the required event fields [Source: .specify/archive/002-sqlite-storage/spec.md -> SC-011]
  and none of the seeded secrets/message bodies or executable terminal control sequences. Captured lifecycle
  diagnostics at every supported log level contain zero QR payloads or session credentials; stdio stdout contains
  zero human-readable pairing or log output. [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> SC-006]
  The "Storage" section of
   `OPERATIONS.md` covers FR-080/FR-082/FR-083 and identifies the owner of encryption, backups, retention and recovery objectives.

- **SC-020**: Scripted logout and unresolved timeout deny a new same-process attempt after production Connect; offline recovery and separately approved pairing preserve conversation records.
  [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> SC-002]

- **SC-021**: Every scripted status transition reports the four required fields consistently; standalone status performs zero network requests and makes zero changes to saved account state.
  [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> SC-003]

- **SC-022**: A six-hour interval refreshes without interrupting a connection. Where same-generation safety is demonstrated, a repeated 405 sequence has at most three delayed retries, no fourth and resets only on confirmed connection; otherwise it produces no unsafe retry and reports unavailability.
  [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> SC-004]

- **SC-023**: All cancellation and shutdown race scenarios demonstrate zero device writes after storage closes and zero lock release before device activity is quiescent.
  [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> SC-005]

- **SC-025**: Offline substitute tests cover every connection-boundary capability in FR-094 without a real account. Simulated command tests cover both `stdio` and `serve`, with and without saved sessions, and observe zero exposed tools, listeners or unsolicited QR attempts in this feature.
  [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> SC-008]

- **SC-026**: Every scripted interruption between app-state version, MAC and contact writes leaves a durable repair indication; restart observes it, success clears only the repaired indication, and failed/interleaved recovery retains it. Tests show app-state repair itself deletes no chats, credentials or session keys and has no unbounded retry loop; separately authorized identity removal under FR-099 may remove only the affected device's credentials/keys while preserving the repair need and conversations.
  [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> SC-009]

- **SC-027**: Synthetic startup failures and timeouts in both shells produce at most three five-second-delayed retries only when same-active-generation safety is demonstrated; otherwise no retry occurs and the limitation is reported. Successful connection or cancellation stops the sequence. Stalled owner-initiated initial and permitted retry, 405 and 515 attempts initiate cancellation at 30 seconds, retain the slot and ownership until drain and never open an unsolicited pairing window.
  [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> SC-010]

- **SC-028**: On the simulator, every scripted orderly stop with no leftover work and no refused calls clears the marker, and the next start connects with zero offline recoveries and zero QR codes. [Source: .specify/archive/004-shutdown-drain/spec.md -> SC-001]

- **SC-029**: Every scripted fault (leftover work at the deadline, a refused network call, a refused storage call, a cut-off observation, a logout without durable cleanup, a crash or a forced exit) leaves the marker unsafe: zero false clears across all fault scripts. Refusal cases that do not fail the check are defined in FR-109. [Source: .specify/archive/004-shutdown-drain/spec.md -> FR-002] [Source: .specify/archive/004-shutdown-drain/spec.md -> SC-002]

- **SC-030**: Across scripted post-pair runs that inject late sends from the pairing generation, zero of them reach the fresh connection. [Source: .specify/archive/004-shutdown-drain/spec.md -> SC-003]

- **SC-031**: A test proves that storage is never blocked while that generation's network can still receive, and that reversing the order fails the test. [Source: .specify/archive/004-shutdown-drain/spec.md -> SC-004]

- **SC-032**: The pass rate of the check is measured over at least 50 simulated orderly stops of each kind (`pair` with post-pair reconnect, `stdio`, `serve`) and recorded in `verification.md`. The bar is at least 90% for `stdio` and `serve` stops, and at least 90% for the pairing generation's check before the post-pair reconnect. A rate below either bar does not fail the feature by itself: it is recorded, and the owner decides before the code review whether to keep option A, tune it, or move to the fork (option B). [Source: .specify/archive/004-shutdown-drain/spec.md -> SC-005]

- **SC-033**: Captured diagnostics at every log level contain zero raw observation data, keys, QR payloads or full account identifiers. [Source: .specify/archive/004-shutdown-drain/spec.md -> SC-006]

- **SC-034**: With separate permission for each step, one real-account run of `pair`, a normal stop, and a restart with the saved session (`stdio`, or the `session-check -action connect` helper) without recovery is recorded with redacted results, or recorded as not verified if not performed. This closes feature 003's open gap "a real reuse of a clean saved session". The observed pass or fail of each check is recorded. [Source: .specify/archive/004-shutdown-drain/spec.md -> SC-007]

- **SC-035**: In synthetic acceptance cases, 100% of valid supported live and history messages are represented with the expected chat, sender, time and direction, subject to explicitly reported storage failures. [Source: .specify/archive/005-message-ingest/spec.md -> SC-001]

- **SC-036**: In the acceptance corpus, 100% of image, video and document captions and supplied media metadata survive ingest separately from message text. [Source: .specify/archive/005-message-ingest/spec.md -> SC-002]

- **SC-037**: All tested mapped LID merges preserve nonduplicate messages and the phone-side duplicate, and a second merge produces no further change. [Source: .specify/archive/005-message-ingest/spec.md -> SC-004]

- **SC-038**: In a repeated-message test for a group with a successfully resolved name, one initial lookup is sufficient and no lookup is made per subsequent message while that name remains available. [Source: .specify/archive/005-message-ingest/spec.md -> SC-005]

- **SC-039**: The synthetic security and shutdown cases show no invalid partial batch, unauthorized account action, secret disclosure or falsely reusable session. [Source: .specify/archive/005-message-ingest/spec.md -> SC-006]

- **SC-040**: Discover all five capabilities and complete scripted chat browsing and contact lookup in one local client session with synthetic data and no separate service. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> SC-001]
- **SC-041**: Stable paging without gaps or repeats across 235 unchanged seeded chats at default and maximum sizes; invalid size/page boundaries produce actionable errors. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> SC-002]
- **SC-042**: Every seeded full, push, first and business name and phone search finds the expected person; aliases are equivalent and partial numbers never choose the wrong person. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> SC-003]
- **SC-043**: Repeated group participation produces one result per chat, tied times select a real latest message, timestamps are standardized date-times and empty collections are consistent. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> SC-004]
- **SC-044**: Zero login/pairing tools discovered; attempts to call either deferred tool fail with no QR disclosure and no account action. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> SC-005]
- **SC-045**: Flood, cancellation and slow-output tests remain within documented byte/work ceilings; no admission-pressure ingest stops and no live tool work after completed shutdown. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> SC-006]
- **SC-046**: At every supported log level, tool output and diagnostics disclose no credentials or QR material; stdout contains zero nonprotocol output. Every rejected abuse case leaves conversation and account state unchanged. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> SC-007]

## Assumptions

- **AS-001**: **Settled decisions used as given:** TOML only (D8); the `WHATSAPP_MCP_` prefix with no aliases (D10); an OS
  file lock on `<data dir>/whatsapp-mcp.lock` that names the holder's PID, implemented with a pure-Go library
  (`gofrs/flock`, D14); the standard library's structured logger (`slog`, M10). [Source: .specify/archive/001-binary-skeleton/spec.md -> "Settled decisions used as given"]

- **AS-002**: **No log format option:** logs are human-readable text on stderr. A JSON option would be speculative
  (Principle IX) and can be added when someone needs it. [Source: .specify/archive/001-binary-skeleton/spec.md -> "No log format option"]

- **AS-003**: **Logs go to stderr in every command**, not only `stdio`: this is the simplest rule and suits service managers,
  which capture stderr. [Source: .specify/archive/001-binary-skeleton/spec.md -> "Logs go to stderr in every command"]

- **AS-004**: **Flags follow the command** (`whatsapp-mcp stdio --log-level debug`), which matches the Claude Desktop config
  `"args": ["stdio"]`. [Source: .specify/archive/001-binary-skeleton/spec.md -> "Flags follow the command"]

- **AS-005**: **`stdio` and `serve` placeholders run until stopped** so that the lock and shutdown can be checked end to end
  with the real binary before the MCP and HTTP features exist. [Source: .specify/archive/001-binary-skeleton/spec.md -> "`stdio` and `serve` placeholders run until stopped"]

- **AS-007**: **Build information** is injected at build time; how is a plan matter. Feature 011 sets it for releases. [Source: .specify/archive/001-binary-skeleton/spec.md -> "Build information"]

- **AS-008**: **CI runs on GitHub Actions**, where the repository is hosted. The Go version comes from `go.mod`. [Source: .specify/archive/001-binary-skeleton/spec.md -> "CI runs on GitHub Actions"]

- **AS-009**: **Out of scope:** generating secrets (C3; features 009 and 010), anything that talks to WhatsApp, the database,
  MCP, HTTP, and releases (011). [Source: .specify/archive/001-binary-skeleton/spec.md -> "Out of scope"]

- **AS-010**: **Settled decisions used as given:** one SQLite file on a pure-Go driver, no PostgreSQL (S1, D2); whatsmeow's
  device store opens on that driver with the dialect name `"sqlite"` and the connection options found in
  spike S1 (`research.md`, appendix); captions in their own field (D18); no import of earlier data (D16). [Source: .specify/archive/002-sqlite-storage/spec.md -> "Settled decisions used as given"]

- **AS-011**: **One device at a time:** the process links one WhatsApp account (§8.2), so contact queries read the device store
  of the current device only. [Source: .specify/archive/002-sqlite-storage/spec.md -> "One device at a time"]

- **AS-012**: **Who decides what is stored:** which WhatsApp messages are worth storing (text or a recognised media type), how
  names are resolved, which quality a name has, and when a LID is merged are decided by ingest (004). The store
  only reads the device store's LID-to-phone map to answer queries (FR-068). The store enforces only identity,
  ordering, name quality, integrity and its own validated resource/input contract. Ingest must handle store
  rejection and bounded history batches without bypassing that contract. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Who decides what is stored"]

- **AS-013**: **Tool defaults versus store bounds:** default page sizes, page numbers and RFC 3339 rendering are applied by
  005 and 006; the store takes an explicit page size and offset and returns time values. Both layers enforce the
  200 base-list cap (D9); the store independently enforces FR-075–FR-078, including context and result-byte limits. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Tool defaults versus store bounds"]

- **AS-014**: **Context at tool defaults is 006's to fit.** Anyone who can message the account can send messages large enough
  that a `list_messages` page with context, at the tools' default sizes, exceeds the 2,000-row or 32 MiB bound.
  The store answers that with `ErrResourceLimit`, as it does for every bound. Feature 006 must not pass that
  failure on to the user at default sizes: it trims or pages the context so the call fits. This is recorded as a
  requirement for 006 in `docs/roadmap.md`. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Context at tool defaults is 006's to fit."]

- **AS-015**: **Generated media filenames** (S5) are built by feature 008 from the stored ID and media type; the store keeps only
  the original file name of documents. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Generated media filenames"]

- **AS-016**: **Sent messages:** whether messages sent through the tools are stored is decided by 007; the store treats them like
  any other message. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Sent messages"]

- **AS-017**: **Out of scope:** the WhatsApp client and pairing (003), ingest and the choice of LIDs to merge (004), the MCP tools (005, 006),
  media download and its cache (008), recipient/approval policy, HTTP/webhooks/converters, and backup/database
  maintenance commands. Storage-compatible backup/restore and decommissioning guidance and offline acceptance
  evidence are in scope; no new service, account action or maintenance command is implied. [Source: .specify/archive/002-sqlite-storage/spec.md -> "Out of scope"]

- **AS-018**: Feature 002 is the storage foundation. Its existing permissions, migrations, lock behaviour and data-directory rules remain binding; this feature extends lifecycle ownership rather than redesigning storage. [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> "Feature 002 is the storage foundation. Its existing permissions,"]

- **AS-019**: The owner confirmed `status` as an offline local inspection for feature 003, not a health probe for another process. It may take the existing lock for safe inspection, but does not create a lock file for absent storage. It fails with a lock-holder error when another process owns the directory; live owner queries remain outside this feature. [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> "The owner confirmed `status` as an offline local inspection for feature"]

- **AS-020**: `pair` ends after successful linking. Same-process re-pair after an upstream attempt is temporarily unavailable; feature 005 cannot bypass the barrier. Running `pair` beside an existing owner still fails on the lock; no implicit proxy is introduced. [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> "`pair` ends after successful linking. Same-process re-pair after an"]

- **AS-021**: The terminal `pair` invocation is the owner's authorization to start one pairing attempt, including automatically supplied replacement codes within it. After an unresolved final timeout, cancellation or end-of-input exits unsuccessfully; Enter does not authorize a new attempt or offline recovery. Future MCP authorization policy is not inferred from tool arguments or authentication alone. [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> "The terminal `pair` invocation is the owner's authorization to start one"]

- **AS-022**: The phone, WhatsApp service and network may delay or refuse linking. No fixed end-to-end pairing duration or guarantee of upstream availability is promised. [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> "The phone, WhatsApp service and network may delay or refuse linking. No"]

- **AS-023**: The bounded acceptance profile below is owner-approved as fixed constants, not configurable settings. Any incompatibility discovered during planning must be raised for explicit review before changing those limits. [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> "The bounded acceptance profile below is owner-approved as fixed constants,"]

- **AS-024**: The owner approved reusing the existing store budgets on device calls, an embedded repair-metadata migration and a separate unsafe-run marker migration. Schema/transport safeguards are mandatory and outside the receive exception; metadata does not imply message ingest or a new account-management command. [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> "The owner approved reusing the existing store budgets on device calls, an"]

- **AS-025**: The check's observation deadlines (one for an ordinary stop, possibly a shorter one before the post-pair reconnect) are fixed constants, not settings. A test-only measurement on the simulator, the first phase of implementation and part of the planning evidence, shows how long leftover library work takes and proposes the values. The owner approves them before any production code uses the check to clear the marker (the library's own bounded wait in its disconnect, 5 seconds, is a reference point, not a decision), and any later change needs the owner's review. [Source: .specify/archive/004-shutdown-drain/spec.md -> "The check's observation deadlines (one for an ordinary stop, possibly"]

- **AS-026**: Stopping the pairing generation and loading the device again between pairing and the fresh connection may change what WhatsApp sees. On the real account the restart request came 0.6 to 0.8 s after the pairing success and the follow-up connection was made about 0.2 s later (feature 003 `verification.md`); the check adds up to its deadline to that gap. This is unverified and needs a permitted real-account run. [Source: .specify/archive/004-shutdown-drain/spec.md -> "Stopping the pairing generation and loading the device again between"]

- **AS-027**: If the permitted real-account run shows that the fresh-client reconnect does not complete a link, the owner decides before the code review whether to fix it, keep feature 003's same-client post-pair reconnect with the marker left unsafe, or stop the feature. Removing the code-level switch (FR-112) removes feature 003's fallback of turning it off, so this decision replaces that fallback. [Source: .specify/archive/004-shutdown-drain/spec.md -> "If the permitted real-account run shows that the fresh-client reconnect"]

- **AS-028**: The existing store block already refuses calls after retirement; this feature adds a count of refusals and other guard or context failures and a matching block for network traffic. The simulator runs in the same process as the client, so its own websocket server goroutines may be counted as library work in measurements; the owner decides at the deadline gate how to treat that. [Source: .specify/archive/004-shutdown-drain/spec.md -> "The existing store block already refuses calls after retirement; this"]

- **AS-029**: The client library version stays pinned. A version change (including a pin past upstream change `2fc844f`) is a separate decision with its own E003-RX-01 reassessment. One such change was made: on 2026-11-09 the owner moved the pin to upstream `f1b7985` (`go.mau.fi/whatsmeow v0.0.0-20261109152745-f1b798561919`) and reassessed E003-RX-01 for it. [Source: .specify/archive/004-shutdown-drain/spec.md -> "The client library version stays pinned. A version change (including"]

- **AS-030**: Feature 003's recovery prompt wording is tracked separately in `docs/backlog/003-offline-recovery-prompt-usability.md` and is not changed here. [Source: .specify/archive/004-shutdown-drain/spec.md -> "Feature 003's recovery prompt wording is tracked separately in `docs/backlog/003-offline-recovery-prompt-usability.md`"]

- **AS-031**: WhatsApp determines the history it supplies; this feature does not promise complete account history or automatic replay after a failed batch. [Source: .specify/archive/005-message-ingest/spec.md -> "WhatsApp determines the history it supplies; this feature does"]

- **AS-032**: The initial history after linking is delivered while `pair` runs; what `pair` has not stored when it stops is lost and reported. [Source: .specify/archive/005-message-ingest/spec.md -> "The initial history after linking is delivered while `pair`"]
  This is a planning assumption, not a delivery guarantee: in a recorded real-account run, no history arrived during `pair`; history arrived on the next `serve` connection. An already-received chunk interrupted at stop is reported incomplete and is not fetched again. [Source: .specify/archive/005-message-ingest/verification.md -> "History after `pair`"]

- **AS-033**: Existing feature 002 storage limits, name precedence and merge decisions apply; the implementation plan must specify bounded ingest admission, cache lifetime, failure reporting and recovery without weakening those limits. [Source: .specify/archive/005-message-ingest/spec.md -> "Existing feature 002 storage limits, name precedence and merge"]

- **AS-034**: Existing feature 004 connection and quiet-stop restrictions remain in force. The receive-resource exception E003-RX-01 was retired on 2026-11-13, when the owner-approved fork that enforces those bounds was pinned (plan, Fork Acceptance Contract). [Source: .specify/archive/005-message-ingest/spec.md -> "Existing feature 004 connection and quiet-stop restrictions remain in"]

- **AS-035**: The feature spec does not authorize real-account acceptance; each step needs separate permission and recorded evidence. [Source: .specify/archive/005-message-ingest/spec.md -> "Acceptance against a real account is not authorized by"]

- **AS-036**: Reuse 002–005 storage, contact normalization, fakeable owner, ingest and quiet-stop safety; no WhatsApp pin, storage budget or reconnect permission change. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "Features 002-005 provide"]
- **AS-037**: The account owner launches a trusted local client under the owner's OS identity, not a multi-user or remote profile. Launch grants all stored chat/contact reads, not pairing or sending. A prompt-injected client could read unrelated chats; configurable restrictions require research and a separate decision. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "Target users are account owners"]
- **AS-038**: Retain list defaults of 30 chats, 20 contact chats and 50 contacts; contact search gains explicit pagination. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "Current list defaults are retained"]
- **AS-039**: Latest-message inclusion defaults true and honors explicit false. Missing dates or messages remain null or absent, not fabricated. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "Optional latest-message inclusion defaults to true"]
- **AS-040**: No compact text format; faithful text alongside structure is mandatory. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "The roadmap's optional compact text format is omitted"]
- **AS-041**: Verify outcomes with synthetic data only; specifications and automated acceptance do not authorize real-account actions. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "Outcome verification uses synthetic data only"]
- **AS-042**: Review concrete transport/resource ceilings before implementation; do not infer numeric approval. Existing store ceilings and the 200-item cap remain binding. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "Concrete new transport/resource ceilings"]

## Scope and References

This feature covers `docs/roadmap.md` 003 and `docs/specification.md` section 8: A3 (`pair` and `status`), A5 (testable connection boundary), P4 (pairing/status state), P5 (version refresh and 405 recovery), M9 (recoverable QR pairing), the WhatsApp log bridge and shutdown portions of M10, and D11 (QR only). Sections 3.4 and 3.6 define preserved events, retry timings and pairing semantics; section 6 defect 16 is explicitly corrected. A2/D14, G1 and constitution Principles I-X remain constraints. Decisions D1-D18 are not reopened. [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> "This feature covers `docs/roadmap.md` 003 and `docs/specification.md`"]

Included: connection lifecycle, shared saved-session use, terminal pairing, local status, current QR availability for future callers, offline substitutes, version refresh, bounded outdated-client recovery and device-write shutdown ordering identified in feature 002's design review. [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> "Included: connection lifecycle, shared saved-session use, terminal"]

Excluded: message/history ingest, LID merging and name resolution (004); MCP tool registration and QR image transport (005); sending and media handling (007-008); HTTP, proxying and daemon status queries (009); webhooks (010); phone-code pairing, new account-management commands, multiple accounts, REST and old-deployment migration. Device-session persistence needed by linking is included; application message persistence is not. [Source: .specify/archive/003-whatsapp-pairing-status/spec.md -> "Excluded: message/history ingest, LID merging and name resolution"]

This feature covers stage 1 of the roadmap follow-up "WhatsApp logout and shutdown drain" and the backlog item `docs/backlog/003-post-pair-reconnect-and-unsafe-marker.md` (debts 1, 2 and 4). It revises the §8.11 entries of 2026-11-04 (temporary fail-closed connection limitation) and 2026-11-06 (post-pair reconnect) for this feature, and keeps M10's shutdown ordering. Decisions D1–D18 are not reopened. [Source: .specify/archive/004-shutdown-drain/spec.md -> "This feature covers stage 1 of the roadmap follow-up"]

Included: the per-generation network block and refusal count, the storage refusal count, the quiet-stop check and its allow-list, clearing the marker after a passed stop, the fresh-client post-pair reconnect, removal of the code-level switch, stop diagnostics, and simulator tests and measurements. [Source: .specify/archive/004-shutdown-drain/spec.md -> "Included: the per-generation network block and refusal count, the"]

Excluded: the 405 budget, startup retries, ordinary reconnect, other 515 reconnects and same-process re-pair (stage 2, a later feature, after the pass rate is known, with a rate limit and a reassessed E003-RX-01); a whatsmeow fork or any other dependency change (option B, a separate owner decision; the 2026-11-09 pin move to upstream `f1b7985` is the one change made); the upstream proposal in the whatsmeow Matrix room (option C, a separate outward-facing step by the owner); replacing the observation with a process exit (an untested idea); message ingest (feature 005); the recovery prompt wording. [Source: .specify/archive/004-shutdown-drain/spec.md -> "Excluded: the 405 budget, startup retries, ordinary reconnect, other"]

Scope of message ingest: live and history persistence, LID normalization and reconciliation, and name acquisition. No sending, new message types, new reconnect policy or old-deployment import; webhook delivery, media download and MCP tools remain separate. [Source: .specify/archive/005-message-ingest/spec.md -> "Sources and scope"]

Local stdio covers the five P1 chat/contact tools, M4–M7, S6, D1, D9, D15 and §4.4, including schema, query and identity defects from §6 items 2, 3, 6, 7, 8 and 13. A5, M10, Q3 and constitution I–X still apply. Use the official SDK and in-memory verification. Wire stdio terminal device-fault handling and reassess ingest pressure without retrying busy ingest writes. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "This feature covers"] [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "Included: local stdio protocol service"]

Excluded: MCP login status/QR and pairing-window controls; message search/context/last-interaction (007), sends/approval (008), media download/cache (009), HTTP/bearer/proxy/`stdio --connect` (010), webhooks (011), and release assurance (012). The `serve` terminal-fault follow-up is separate. No phone-code pairing, same-process re-pair, new reconnect/recovery authority, multiple accounts, migration/import or REST. [Source: .specify/archive/006-mcp-stdio-tools/spec.md -> "Excluded: `get_login_status`"]
