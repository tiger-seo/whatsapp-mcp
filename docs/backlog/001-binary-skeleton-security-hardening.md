# Backlog: security hardening of feature 001

- **Created:** 2026-10-27
- **Status:** Implemented on branch `001-security-hardening` (2026-10-27), not yet committed, pushed or run in CI. Results and open risks: `specs\001-binary-skeleton\verification.md`, section "Security hardening follow-up"
- **Affected feature:** `specs\001-binary-skeleton`
- **Owner:** Maintainer (tiger-seo)
- **Approved choices (2026-10-27):** refuse an existing non-private data directory and print the fix, never change it; on Windows allow only this user, SYSTEM and Administrators; config cap 1 MiB; work on a new branch from `main`
- **Source:** [Security review](../../2026-10-27_security_review.md), especially F03–F05, F09–F11 and F15
- **Baseline:** [Specification §8](../specification.md#8-rewrite-requirements-go-no-docker) and [roadmap feature 001](../roadmap.md#001-skeleton-cli-config-logging-lock-ci)

## Decision summary

**Yes: the implemented skeleton needs targeted security-hardening follow-ups, not a rewrite.** Five work packages below address its data-directory, lock, configuration and build/CI foundations. They are not five confirmed exploitable vulnerabilities, and they do not transfer all 16 specification-review findings into feature 001.

Feature 001 can remain completed against its original acceptance criteria. These are additional proposed security requirements; approve the relevant specification/contract amendments before implementing them. In particular, retaining existing directory permissions is intentional current behavior, not an accidental failure to implement the original contract.

This entry is based on a targeted static inspection of the feature specification/contracts, configuration and CLI source/tests, lock source/tests, and build/CI files. No application tests, security reproductions, dependency scans, platform ACL checks or real-account operations were performed for this entry. Repository-host security settings were not inspected. Code-path observations below are not runtime exploit demonstrations or a complete implementation audit.

## Existing safeguards to retain

- `internal\config\datadir.go:33–53` creates missing directories with requested POSIX mode `0700`; this protection is already present. Existing directories are deliberately left unchanged. Windows access control is not established by those mode bits.
- `internal\config\config.go:78–114` validates layered settings before directory creation; unknown TOML keys/types are rejected by `internal\config\file.go`. Keep flag > environment > file > default precedence, strict validation, and working-directory independence.
- `internal\config\config.go:59–75` already masks settings marked `Secret` in debug dumps. There are currently only two settings, `data_dir` and `log_level`, neither secret (`internal\config\setting.go:45–71`). No existing HTTP-token leak is asserted.
- `cmd\whatsapp-mcp\commands.go:16–25, 101–123` keeps `version` independent of configuration/files and sends operational logs to stderr. Keep protocol stdout clean, lifecycle behavior, and exit-code categories.
- The lock uses `gofrs/flock`, nonblocking acquisition, a holder PID, and OS release on process death. Preserve D14, including the Windows byte-zero lock/PID-offset convention; the lock is a consistency mechanism, not an authorization boundary.
- `.github\workflows\ci.yml:7–16` already restricts its token to `contents: read` and sets `CGO_ENABLED=0`. It uses `pull_request`, not `pull_request_target`, and its visible jobs contain no secret references. Exact direct module versions and `go.sum` are already present. Retain the required checks, three-OS tests and five-target cross-builds.

## Work packages and ordering

`P1` means schedule before the named sensitive capability is enabled; `P2` means complete before wider deployment, or record an explicit risk acceptance. These are backlog priorities, not new severity scores. The original review's ratings remain unchanged.

| ID | Work package | Priority / gate | Review relationship |
| --- | --- | --- | --- |
| 001-SEC-01 | Private, verified data directory and configuration trust boundary | P1; before 002 creates credentials/message storage | F03; foundational portion of F05 |
| 001-SEC-02 | Safe lock-file opening and stable object identity | P1; before 002 relies on exclusive ownership | Foundational portion of F05; supports F03 |
| 001-SEC-03 | Secret-safe diagnostics across all configuration paths | P1 before secret settings in 009/010; preferably establish the contract now | F04, F11; diagnostic portion of F10 |
| 001-SEC-04 | Bounded, regular-file configuration input | P2; before wider deployment of the skeleton | Local-startup portion of F09/F10 |
| 001-SEC-05 | Dependency/CI checks and consistent CGO-free local builds | P1; start in the next maintenance change and retain continuously | CI portion of F15; existing build invariant |

### 001-SEC-01 — Verify private storage roots on Unix and Windows

**Observed evidence**

`PrepareDataDir` uses `os.MkdirAll(..., 0700)` and `os.Stat`, without checking existing ownership/access or Windows ACLs (`internal\config\datadir.go:33–53`). `TestPrepareDataDirKeepsMode` explicitly requires a preexisting `0755` directory to remain unchanged, and skips Windows mode checking (`internal\config\datadir_test.go:84–100`). `config.Load` reads `config.toml` before `PrepareDataDir` runs (`internal\config\config.go:89–104`; `cmd\whatsapp-mcp\commands.go:106–119`). A check added only after loading config would therefore be too late to establish trust in that input.

**Required follow-up**

- Define and enforce supported ownership/access rules for new and preexisting data roots. On Unix, require the intended process identity and private permissions; on Windows, create/verify a restrictive DACL for the intended user/service identity, documenting required administrative/system access and handling inherited grants.
- Define supported behavior for directory symlinks, junctions/reparse points, ancestor replacement and unsafe shared locations. Reject unsupported/unsafe cases with actionable diagnostics; do not silently change arbitrary operator-owned directories or recursively change unrelated files.
- Establish a read-only trust check before opening an existing `config.toml`, then safely create/verify the root after configuration validation. Do not create directories on invalid configuration, and do not make `version` inspect storage. Define ownership/access checks for the existing config file itself, not just its directory.
- Provide a CGO-free foundation usable by 002 and later artifact creators. Directory privacy does not by itself close database, sidecar, media, secret-file or backup requirements; their creators must apply and verify appropriate protections separately.

**Acceptance evidence**

- Unix and Windows tests cover missing/private roots, permissive preexisting roots, wrong ownership/access where testable, inherited ACLs, and supported link/reparse cases. Unsafe roots fail before config is trusted or sensitive artifacts are created; errors identify the remedy without changing unrelated paths.
- A separate-identity check demonstrates denied access to the protected root/config on each platform. Unit tests alone are not evidence of Windows effective ACL enforcement; record any test-environment limitations explicitly.
- Approve the changed permissions contract and update the existing preserve-mode test accordingly. Preserve configuration precedence, no creation on invalid input, and file-free `version` behavior.

**NIST relevance:** AC-3, AC-6, SC-28. Disk/backup encryption remains an operator responsibility to document, not an automatic property of this helper.

### 001-SEC-02 — Prevent substituted lock paths from redirecting writes

**Observed evidence**

`internal\lock\lock.go:50–74` acquires a flock by pathname, opens that pathname again, truncates it and writes the PID through the second handle. `Release` truncates that second handle (`:77–83`). There is no explicit link/type/object-identity check in this path. The `0600` argument to the second `os.OpenFile` does not reset existing permissions. This is a local-filesystem substitution concern, especially with unsafe directory access; no successful attack was reproduced.

**Required follow-up**

- Create/open the lock artifact with appropriate permissions and reject unsupported links/reparse points and non-regular objects before destructive operations. Ensure that the OS lock and PID-writing handle refer to the same intended file throughout acquisition and release.
- Use a race-resistant approach under the documented supported filesystem/threat model. An earlier pathname check or symlink resolution alone is not sufficient. Coordinate with 001-SEC-01 so another untrusted identity cannot replace entries in the root.
- Preserve D14 and the existing library unless a demonstrated technical constraint requires an explicitly approved change. Do not substitute PID-only locks, delete/recreate an active lock, or claim that the lock protects separately copied data directories.

**Acceptance evidence**

- Negative tests use sentinel files to show that lock-path symlinks, Windows reparse points where supported, directory/special-file entries, and check/open substitutions cannot truncate or modify an outside target. Include second-handle object mismatch and relevant hard-link restrictions in the supported threat model.
- Retain cross-process contention/PID, crash-release, repeated-release and Windows PID-read behavior. On unsafe acquisition, fail without touching an unrelated object or leaving a usable second owner.

**NIST relevance:** AC-3, AC-6, SI-10.

### 001-SEC-03 — Extend masking beyond the successful debug dump

**Observed evidence**

`Setting.Secret` promises to keep values out of logs/output (`internal\config\setting.go:31–32`), and `LogValues` implements debug masking. However, `resolve` places raw rejected values into `SettingError` (`internal\config\config.go:119–123`); file-value validation does likewise (`internal\config\file.go:123–135`), and `SettingError.Error` prints them (`internal\config\errors.go:17–18`). Parser reasons are forwarded (`internal\config\file.go:44–50`). Help prints defaults without a secret-specific branch (`cmd\whatsapp-mcp\cli.go:120–125`). The configuration contract currently requires the rejected value in every validation error (`specs\001-binary-skeleton\contracts\configuration.md:44–68`). These are latent integration gaps before secret descriptors are introduced, not evidence of credentials already leaking.

**Required follow-up**

- Amend the contract so secret values are excluded from validation errors, parser/type failures, wrapped reasons, debug logs, help/default rendering and all pre-logger diagnostics. Preserve useful setting/source/location information; validation text must not embed the rejected secret indirectly.
- Safely encode untrusted configuration keys, paths and diagnostic content in terminal output; retain intentional formatting but prevent input-supplied control sequences from injecting terminal actions or fabricated lines.
- Add tests through the existing registry-based design without introducing production token settings or secret-generation commands early. The deliberate owner-facing secret-print command remains future 009/010 work with its own explicit output boundary.

**Acceptance evidence**

- Synthetic secret descriptors/sentinels exercise flag, environment and TOML success/failure paths, malformed TOML, help/default output, joined errors and all log levels. Assert sentinel absence from stdout/stderr, including before logger creation, and confirm that ordinary nonsecret diagnostics remain informative.
- Verify control-character encoding and empty protocol stdout on failing `stdio` startup. Cover the future secret-setting integration again when 009/010 add those descriptors.

**NIST relevance:** IA-5, AU-9, SI-10. A complete security audit trail is later capability-specific work, not supplied by this change.

### 001-SEC-04 — Bound configuration reads and parsing

**Observed evidence**

`internal\config\file.go:29–52` reads the entire config path using `os.ReadFile` and then decodes it. It does not impose an application byte ceiling or require a regular file. A huge config can consume startup memory; a FIFO/device or substituted path can block or produce unbounded input on platforms supporting those objects. The input is local operator configuration, not a remotely exposed MCP request.

**Required follow-up**

- Require a supported, safely opened regular config file, coordinated with the root/object checks above. Bound the actual bytes read, not only a previously checked file size, and reject oversized input before TOML decoding.
- Approve and document a numeric cap before implementation (proposed starting point: 1 MiB; this is an engineering choice, not a NIST value). Ensure errors do not dump file contents. Account for concurrent file growth and unsupported special files without blocking startup indefinitely.
- Preserve missing/empty-file behavior, strict keys/types and multi-source error reporting. Do not add unrelated HTTP/media/session quotas to the two-setting skeleton.

**Acceptance evidence**

- Test the approved cap and cap-plus-one, growth during reading, malformed/deep TOML within the cap, wrong file types, supported link cases and prompt failure for unsupported special files. Record expected resource/time bounds for these tests.
- Keep existing configuration/CLI regression tests; add focused fuzz coverage for configuration parsing/diagnostic formatting with synthetic inputs.

**NIST relevance:** SC-5, SI-10, CM-6.

### 001-SEC-05 — Harden the existing build and CI boundary

**Observed evidence**

The workflow already has limited permissions and CGO disabled. Actions are nevertheless referenced by mutable version tags (`.github\workflows\ci.yml:24–27, 71–74`), and its checks do not include `govulncheck` or a secret-scanning step (`:28–59, 75`). GitHub-native secret scanning/push protection and branch protection may already exist, but were not verified. `build.sh:1–4` invokes `go build` without enforcing `CGO_ENABLED=0`; that does not prove the present binary uses CGO, but it leaves this entry point dependent on the caller's environment.

**Required follow-up**

- Pin third-party actions to reviewed full commit SHAs, document their intended versions, and define an update/review process. Retain read-only workflow permissions and isolation of untrusted pull requests from release credentials.
- Add explicit `go mod verify` and a version-pinned `govulncheck` invocation compatible with the selected Go toolchain and `CGO_ENABLED=0`. Define failure/triage rules and record scan dates/results; checksum verification and vulnerability scanning solve different problems. Do not describe unscanned dependencies as vulnerable or safe.
- Verify GitHub-native secret scanning/push protection and required-check settings. Enable appropriate supported protection, or document a reviewed alternative if unavailable; lack of a YAML scanner step alone is not proof that hosted protection is absent.
- If `build.sh` remains a supported entry point, make it explicitly enforce `CGO_ENABLED=0`; otherwise retire/document it through a separately reviewed change. Do not leave a documented build route dependent on the caller's CGO setting.

**Acceptance evidence**

- CI records module verification and vulnerability-scan results alongside existing formatting, vet, tests and cross-builds. Demonstrate detection/failure behavior with controlled fixtures or isolated validation, not real credentials or a vulnerable production dependency added just for testing.
- Review action SHA identities and repository-setting evidence. Confirm supported local builds disable CGO even when the caller enables it, and retain all five target builds and three-OS tests. Do not mandate `go test -race` under the project's always-CGO-off rule.

**NIST relevance:** RA-5, SI-2, SA-11, SR-3. These checks do not authenticate a release or close all of F15.

## Boundaries: do not pull later features into 001

| Later owner | Remaining security work |
| --- | --- |
| 002 storage | Private database/WAL/SHM creation and verification, parameterized queries, safe migrations/newer-schema refusal, disk-full handling and coherent backup/recovery tests (F03, F09, F10, F13). Use the hardened root; do not treat it as sufficient protection for every artifact. |
| 003–006 connection, ingest and reads | Pairing authorization/window, untrusted-content handling, bounded history/read output and capability-specific audit events (F01, F09–F12). |
| 007–008 sending/media | Recipient/approval policy only after an explicit owner decision; actual send-path containment, composite cache namespace, parser/converter restrictions and media limits (F01, F05, F06, F09). |
| 009 HTTP/proxy | TLS/Origin/host policy, all-method bearer enforcement, token entropy/atomic creation/rotation/revocation and HTTP/session limits (F02, F04, F09). No HTTP listener or token generation exists in the skeleton. |
| 010 webhook | Destination/TLS policy, HMAC key lifecycle, signed identity/replay/deduplication guidance and bounded delivery (F04, F07, F08). |
| 011 releases and owner operations | Signed/attested releases, provenance/inventory and consumer verification; retention, backup, incident and deployment/control-ownership records (F11–F16). CI hardening starts now; release signing does not belong in the artifact-free 001 workflow. |

Feature 001 closure does not close the parent security findings for later capabilities. Preserve D1–D18, the single Go module/binary, SQLite-only architecture, current command names and always `CGO_ENABLED=0`; do not introduce Docker, PostgreSQL, JWT, import, or a new sending-approval product requirement through this backlog.

## Definition of done for this follow-up

- [X] Assign an implementation owner and approve the security requirements, supported filesystem/identity model, numeric config cap and any compatibility changes to the original contracts.
- [ ] Implement and verify the five work packages with synthetic data. Run relevant config/CLI/lock tests first, then existing full CI checks; retain real OS-specific ACL/path evidence and record limitations. *Local checks done on Windows and Linux (WSL); CI on GitHub not yet run.*
- [ ] Record results, unresolved risks and named, expiring exceptions. Gate storage on 001-SEC-01/02 and secret-setting integration on 001-SEC-03; do not mark an untested platform control as passed. *Results and risks recorded; no expiring exceptions named yet (the Go 1.27.0 stdlib entries are the candidate).*
- [X] Keep the original security report intact and track remaining feature-specific work separately. NIST mappings here indicate relevance, not baseline satisfaction, certification or FIPS validation.
