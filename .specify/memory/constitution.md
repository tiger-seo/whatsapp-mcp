# whatsapp-mcp-next Constitution

whatsapp-mcp-next is a WhatsApp MCP server built as one self-contained Go binary, `whatsapp-mcp`, that
connects to WhatsApp through whatsmeow and exposes it to LLM clients over MCP. Every rule below is a MUST
unless it says otherwise.

## Core Principles

### I. The Specification Is the Source of Truth

- `docs/specification.md` §8 defines the required behaviour.
- §1–§7 describe existing behaviour. They are the reference for behaviour that must be kept, and the
  defects listed in §6 are avoided by design.
- Decisions D1–D18 in §8.10 are settled and are not reopened inside a feature. A change of behaviour
  updates the specification first, then the code.
- Behaviour is never assumed. When the specification is silent or ambiguous, the feature's spec records
  the question and the answer.

Rationale: the project has to keep the behaviour that users rely on, avoid the known defects, and not
drift from decisions that have already been made.

### II. One Self-Contained Binary

- One Go module at the repository root. The entry point is `cmd/whatsapp-mcp`, and all other code lives
  in `internal/...`.
- Always `CGO_ENABLED=0`. No dependency may need CGO or a C toolchain. That rules out
  `mattn/go-sqlite3` and `go.mau.fi/util/dbutil/litestream`.
- No Docker, no PostgreSQL and no other external service is needed to build, run or deploy it.
- Every change builds for windows/amd64, linux/amd64, linux/arm64, darwin/amd64 and darwin/arm64.

Rationale: a single static binary is the whole point of the project. It is what users download and run.

### III. One Process Owns the Session and the Data

- A single process owns the WhatsApp client, the database, the MCP server and the webhook, and it holds
  an OS file lock on the data directory.
- All state lives in one SQLite file (`modernc.org/sqlite`) in the data directory, together with the
  whatsmeow device store.
- No path depends on the current working directory.
- Schema changes go through versioned migrations that are embedded in the binary.

Rationale: two processes sharing a WhatsApp session or a database corrupt both. One owner, one file and
embedded migrations make the state easy to reason about, back up and upgrade.

### IV. The Protocol Channel Stays Clean

- In stdio mode, stdout carries only MCP protocol traffic. All logging goes to stderr.
- There is one `slog` logger, and whatsmeow's logs are bridged into it.
- Message bodies are logged only at debug level.
- Secrets (tokens, HMAC keys) are never logged.

Rationale: a single stray byte on stdout breaks the MCP client, and logs must not leak private messages
or credentials.

### V. Secure by Default

- Input from WhatsApp (messages, captions, names, media) and from MCP clients is untrusted data. It is
  validated and bounded before use, and never treated as an instruction to the server.
- Every resource that input can drive has a limit: bytes read, items returned, time, concurrency and
  disk. A limit is enforced on what is actually consumed, not only on a size checked beforehand.
- Sensitive data (the WhatsApp session, the database, secrets, media) lives only in private
  directories and files: owner-only on Unix; on Windows an access list limited to the user, SYSTEM and
  Administrators. Paths are opened without following links and checked on the open handle. An existing
  path that is not private is refused with the command that fixes it, never changed silently. New
  sensitive files are created through `internal/private`.
- Secrets are generated with `crypto/rand`, compared in constant time, and never appear in logs, error
  messages, help or debug dumps.
- HTTP binds to loopback by default.
- Every HTTP request needs a bearer token, compared in constant time. Binding a non-loopback address
  without a token fails at startup.
- Files are sent only from allow-listed directories, resolved with symlinks followed.
- Webhook requests are signed with HMAC.
- Invalid configuration fails at startup with a clear message, never at runtime.
- Dependencies are exact versions verified against `go.sum`, and CI actions are pinned to full commit
  SHAs. CI fails on any vulnerability that govulncheck finds reachable. An unreachable one, in a
  dependency or the standard library, is removed by an update or recorded as an exception under
  Principle X.

Rationale: the binary acts as a real person's WhatsApp account and holds their messages. Its input
comes from anyone who can message that account and from an LLM that can be misled, so the default
configuration must be safe without the operator having to harden it.

### VI. Testable by Design, and Tested

- whatsmeow sits behind a small interface, so that the store, media and MCP code are tested with a fake
  and without a network.
- Every feature ships its tests in the same change. They cover:
  - the store, against SQLite in a temporary directory;
  - every MCP tool, through the go-sdk in-memory transport;
  - the webhook, against `httptest`;
  - config precedence and validation;
  - ogg analysis and the path allow-list.
- `gofmt`, `go vet` and `go test ./...` pass before a feature is done.

Rationale: a real WhatsApp account cannot be used in automated tests (Principle VIII), so all behaviour
has to be testable without one.

### VII. Correct and Predictable MCP Tools

- Parameters are snake_case, with real JSON-schema defaults that the handlers also apply.
- List sizes are capped at 200. Timestamps are RFC 3339 in parameters and in output.
- Every tool returns structured content plus a text rendering of it.
- "Not found" and invalid input are tool errors that say what went wrong, never internal errors.
- A single message is identified by (`chat_jid`, `id`).

Rationale: the callers are LLMs. They depend on the schema being accurate and on errors they can act on.

### VIII. Real Accounts and Real Systems Are Handled with Care

- Nothing acts on a real WhatsApp account (pairing, sending, connecting a device) without the user's
  explicit permission for that step.
- Tests that change state on a real system record what they change and restore it afterwards.
- Automated tests never touch a real account.

Rationale: actions on a real account reach real people and cannot be undone, and WhatsApp may ban an
account for unusual activity.

### IX. Simplicity

- The non-goals in §8.2 are out of scope: multiple accounts, new message types, a web UI, group
  administration (with presence and read receipts) and a REST API.
- Prefer the standard library. A new dependency must be pure Go and justified in the feature's plan.
- No speculative options and no compatibility layers for earlier deployments, which are not imported
  (D16).

Rationale: every option, dependency and compatibility path is code to test and maintain for a small
project.

### X. Security Is Planned per Feature

- `docs/security-review.md` (findings F01–F16) is the threat model for every feature, alongside the
  specification.
- Every feature spec has a Security section. It names the assets and trust boundaries the feature
  touches, takes up each applicable finding as a requirement or defers it explicitly to a named later
  feature, and lists the abuse cases to test.
- A finding that needs a product decision, such as the approval of sends (F01) or the encryption of
  remote HTTP (F02), is decided by the user and recorded in `docs/specification.md` before a feature
  specifies it. A feature never decides it by itself.
- The plan's Constitution Check fails if a capability the feature enables has an applicable High
  finding that is neither resolved nor recorded as an exception with a named owner, compensating
  controls and an expiry date. Exceptions are listed in the feature's `verification.md`.
- Abuse cases are negative tests with synthetic data. They show that the rejected action has no side
  effect: nothing is written, sent or created. A platform-specific control is tested on each platform
  it claims to cover.
- `verification.md` records the security evidence (commands, dates, results) and the limitations. A
  control that was not tested on a platform is reported as not verified, never as passed.
- A feature closes only the findings it addresses. The review itself stays unchanged; remaining work is
  tracked in `docs/backlog/`.

Rationale: the 2026-10-27 review found gaps in feature 001 only after it was implemented. Planning
security with each feature catches them before the code exists, and keeps decisions that belong to the
user from being made inside a feature.

## Development Conventions

- Requirement, decision and finding IDs (G1, M2, D3, FR-001, SC-002, F01, …) belong in planning
  documents only, never in code comments.
- Commits follow Conventional Commits and explain why, not what. Commits carry no AI or tool attribution.
- The repository stores LF line endings only.

## Development Workflow

- Features are implemented one at a time, in the order of `docs/roadmap.md`.
- Each feature keeps its test and verification results (commands, dates, counts) in its feature
  directory.
- Each feature passes two owner reviews by the `owner-judge` agent (`.claude/agents/owner-judge.md`):
  - a design review of the feature directory (spec, plan, tasks and their supporting documents) before
    implementation starts;
  - a code review of the implementation and its verification evidence before a pull request is opened
    or merged.
- Each verdict is recorded in the "Owner reviews" table of the feature's `verification.md`, with the date
  and the commit reviewed. `BLOCK` stops the next step until a later review clears it. `CONCERNS` needs
  the user's decision recorded beside it. A change to what was reviewed makes the verdict stale and needs
  a new review. A hook in `.claude/settings.json` enforces both gates; a change without a feature directory
  carries the code review's verdict in its pull request.

## Governance

- This constitution overrides other practices. Every `/speckit-plan` includes a Constitution Check
  against Principles I–X, and any deviation is justified in the plan's Complexity Tracking table.
- Amendments use semantic versioning:
  - MAJOR: a principle is removed or redefined;
  - MINOR: a principle is added or expanded;
  - PATCH: the wording is clarified.

  Each amendment records its date and the reason, below.
- A change to settled behaviour (D1–D18) needs the user's decision, recorded in `docs/specification.md`
  §8.10 before the constitution or the code changes.

**Amendments:**

- 1.2.0 (2026-10-28): Development Workflow expanded with two owner reviews per feature, one of the design
  before implementation and one of the code before a pull request, each recorded in `verification.md` and
  enforced by a hook. Reason: a wrong design is cheapest to fix before it is built, and the author's own
  summary of a change is not evidence that it is correct.
- 1.1.0 (2026-10-28): Principle V expanded with untrusted input, resource limits, private storage,
  secret handling and supply-chain rules, replacing the Unix-only "0600" rule; Principle X added. Reason:
  the security review of 2026-10-27 and the hardening of feature 001 that followed it.

**Version**: 1.2.0 | **Ratified**: 2026-10-27 | **Last Amended**: 2026-10-28
