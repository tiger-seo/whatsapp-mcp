# Contract: Pair, Status and Connection-Owning Shells

## Shared Rules

Keep existing configuration precedence, registry, exit categories (0 success, 1 operational failure, 2 usage/config) and private-file diagnostics. No new flag, listener, proxy, import, phone-code or account-management command. version remains configuration/file independent. All logs go to stderr; stdio stdout remains empty until feature 005 supplies protocol traffic.

Connection-owning commands acquire the directory lock before writable Open/migrations/device use, register safe cleanup, fetch version before connection and use the owner contract. Existing signal and Windows console behaviour remain, except timeout can no longer release active storage. Second signal remains forced/unclean.

## pair

1. Load validated configuration, prepare private directory, acquire lock and open/migrate store. Check and durably arm the run marker before creating an account-capable client; refuse unsafe/unknown marker or locked storage before account work, retaining holder PID errors.
2. Clean saved identity: attempt connection, no QR or identity replacement; succeed only on Connected, then safe shutdown. Failure does not erase identity.
3. Clean state with no identity: explicit invocation authorizes one pairing attempt. Render current QR only in this terminal command, with a fixed quiet zone/no input-supplied control sequence. Invalidate old code on replacement/expiry. Never log/persist raw material.
4. One valid code is at most 4 KiB with lifetime min(upstream expiry,60 seconds). Replacement codes rotate automatically within the same approved attempt. One upstream channel consumer serves all observers.
5. An unresolved final timeout exits 1 after cleanup with the unsafe marker retained and an offline-recovery diagnostic. Enter, EOF or cancellation cannot start a new attempt or authorize recovery; no automatic retry loop. Read any recovery input only through the injected stream, with cancellation and no competing protocol reader or unbounded input buffer.
6. Unsupported phone-code/passkey flows fail with a safe diagnostic rather than auto-confirming. PairSuccess is not command success until saved identity plus Connected. On success clear QR, report linked/already paired and safely stop, exit 0.
7. Pair success does not claim complete contact/history synchronization. If repair remains pending, use a safe warning and retain its marker for a later authorized owner run. Storage/link failure exits 1; never present an undurable pairing as successful.
8. Unsafe/legacy-unknown state: remain offline and fail unless the operator explicitly selects the recovery branch from a real interactive terminal; an ordinary invocation, redirected input, Enter, EOF or cancellation alone is not approval. Identify the account/device and require the operator to verify the phone, separately authorize and perform any required phone-side unlink, and separately authorize local identity removal if a row exists. If there is no row, still verify there is no orphaned phone-side link. Only after successful verification and any authorized removal may the recovery transaction clear the marker. Recovery exits 0 offline without connecting or showing a QR; a later fresh pair requires a separate invocation/approval. Refusal, unverifiable identity or definite failed persistence exits 1 with the block retained. An ambiguous commit exits 1 with no account work and requires reopening to inspect committed state; clean can appear only after the authorized prerequisites were met. No remote unlink is performed by the application.

Only this command's stdout may contain human QR/status text. No QR/control material may reach stdio stdout via shared code. Real execution needs separate user permission for each account-affecting attempt in validation.

## status

No Connect, discovery, pairing, IPC, store creation, migration, repair or credential validation with WhatsApp.

Stable stdout fields, one per line:

```text
connected: false
logged_in: true
pairing_required: false
wa_version: 2.3000.1050728964 (bundled; not refreshed)
```

The version literal above is the current pin's fallback, not hard-coded forever across approved dependency upgrades. logged_in is false when no saved identity exists. Stderr warns `Offline snapshot; saved credentials have not been verified with WhatsApp.` when identity exists, and may contain existing safe config/log warnings.

| Storage/owner condition | Required outcome |
| --- | --- |
| Missing directory | Exit 0 unpaired snapshot; create nothing. |
| Existing directory, no database/sidecars/lock | Exit 0 unpaired point-in-time snapshot; create nothing. |
| Existing held lock, even before DB creation | Exit 1 with holder PID; no database inspection or claim about the owner's live status. |
| Existing free lock | Acquire through no-create path, inspect under ownership; existing PID bookkeeping allowed, no database/sidecar changes. |
| Present DB but absent lock | Establish normal owner lock, recheck artifacts and inspect under ownership; only lock metadata may be created. |
| Clean schema-3 unpaired/paired committed DB/WAL | Exit 0, identity-presence boolean from validated read-only committed view; no key loading or repair. |
| Unsafe schema-3 or readable schema-1/2 legacy DB/WAL | Exit 1 with offline-recovery-required warning and four fields reflecting local identity presence; no migration, key loading, network or repair. Legacy is unknown, not automatically clean. |
| Missing/malformed schema-3 marker | Exit 1 classified unsafe-storage diagnostic; do not infer clean state or mutate artifacts. |
| Orphan sidecar, unsafe/substituted/corrupt/unsupported schema | Exit 1 existing classified diagnostic; no repair, upgrade or unrelated-target mutation. |

AcquireExisting must retain Acquire's no-link/open-handle/same-file rules and return fs.ErrNotExist on absent lock rather than create it. Basic missing-artifact observation never reads a present database without ownership. Recheck after acquisition handles a creator racing status; a missing result is a local observation, not a global guarantee no owner can start afterwards.

## stdio and serve in Feature 003

Both remain existing command shells, acquire private storage ownership and, only with a clean marker, arm it and reuse the saved session or remain running unpaired. Unsafe/legacy-unknown state exits 1 with an offline-recovery diagnostic and no account work. A saved-session initial failure or timeout permits three retries after cancellable five-second waits; exhaustion leaves the shell running disconnected with an actionable diagnostic and preserved identity. Stop the startup sequence on Connected, shutdown, logout, storage fault or handoff to the separate 405 controller. No automatic QR or pairing window, MCP tools, HTTP listener or webhook is exposed by this feature. Version discovery and lifecycle are active; stdin EOF stops stdio. Retain truthful status during disconnect/logout/recovery.

Later features add transports through the same owner boundary. No early IPC/query path is added for status; running pair/status beside an active owner still fails on its lock. The manual testdata harness, not a shipped new command, demonstrates same-process re-pair before tools exist.

## Error and Evidence Rules

Use existing safe path/config/storage classifications and bounded owner reason codes; never raw QR, JID, key, body, HTTP response or SQL text. Failure/timeout changes exit result but does not authorize lock release with active writes. Record manual permissions, device changes and cleanup without secrets. Offline tests use a forbidden-network provider/factory and isolated synthetic data; existing stub-era tests and the CI privacy fixture must be deliberately updated.
