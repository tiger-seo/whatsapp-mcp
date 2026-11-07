# Backlog: the offline recovery prompts are hard to follow (feature 003 usability)

- **Created:** 2026-11-07
- **Status:** Open. Not started; no wording or code was changed.
- **Affected feature:** `specs/003-whatsapp-pairing-status` (US1 offline recovery, `cmd/whatsapp-mcp/recovery.go`, `contracts/cli.md`, `OPERATIONS.md` "Offline recovery").
- **Owner:** Maintainer `tiger-seo`.
- **Review trigger:** before the full-branch code review of feature 003, so the review covers the new wording. A change to the reviewed contract also needs a fresh owner design review.
- **Source:** the first real run of the recovery path (T062, 2026-11-07), where the user could not tell what to do at the identification prompt.

## Evidence

At the prompt "Identify the exact account and linked device using your own evidence… type absent if the relevant entries are absent, or unlink if manual removal is needed", the user said they did not understand what they needed to do. The text does not say where to look (WhatsApp, Linked devices), what to look for, or what each answer does. The answers (`absent`, `unlink`, `authorize-unlink`, `verified`, `remove`) read as internal terms.

## Proposed changes

1. Open with a plain summary: recovery removes the saved login from this computer, keeps chats and messages, never contacts WhatsApp and never touches the phone.
2. Ask the real question: open WhatsApp, Linked devices; is there still an entry for this computer? Use answer words that match the question (for example `none` and `still-there`) instead of `absent` and `unlink`.
3. Say what to look for: show, from local data, the last four digits of the account and when it was paired. State that the phone label is not reliable and what that means for the choice.
4. Before each question, say in one line what each answer does, and that `Ctrl+C`, Enter or end of input is safe and changes nothing.
5. Show progress ("Step 1 of 3") and, after each answer, say what happens next.
6. Keep the exact-typed answers. Typing a specific word is a deliberate safety measure; only the words and the surrounding text change.

## Constraints

- No relaxation of the safety rules: interactive terminal only, bounded input, exact-match answers, no unlinking by the program, no connection during recovery.
- The account digits come from the local store only; print the last four digits at most and never the full number, a JID or any key material.
- Update `contracts/cli.md`, `OPERATIONS.md` and the tests that assert the prompt text (`cmd/whatsapp-mcp/recovery_test.go`, `internal/store` recovery tests if they match text) in the same change, tests first.

## Closure

Close after the new prompts pass the existing safety and abuse tests, a fresh owner design review accepts the changed contract, and a person who has not seen the flow before completes a recovery on a throwaway directory without help.
