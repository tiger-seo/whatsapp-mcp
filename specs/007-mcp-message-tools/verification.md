# Verification: MCP Message Tools

## Planning Evidence

| Date | Check | Result |
| --- | --- | --- |
| 2026-11-22 | `.specify/scripts/bash/setup-plan.sh --json` (Git Bash on Windows) | Exit 0; resolved branch `007-mcp-message-tools` and generated the plan template in this directory. |
| 2026-11-22 | Read-only audit of `internal/store`, `internal/mcp`, the feature spec and the 002/006 baselines | Two independent research passes identified the indexed seek paths and the existing reply/lifecycle bounds. Research decisions are recorded in [research.md](research.md). No SQLite measurements, runtime tests or operator-account actions were run. |
| 2026-11-22 | Task checklist validation with `git grep --no-index` and PowerShell | Exit 0; 36 sequential unchecked tasks with correct story labels and file paths. `git show --check 0d4c7fa` and `git ls-files --eol -- specs/007-mcp-message-tools/tasks.md` passed. Documentation checks only. |

## Runtime and Security Evidence

Not verified. The indexed query and driver-return feasibility checks, actual heap and reply bounds, SDK acceptance and native stdio tests are required as described in the [store contract](contracts/store-and-lifecycle.md) and [quickstart](quickstart.md). No security exception or real-account verification is in scope.

## Owner reviews

| Stage | Date | Commit | Verdict | Owner decision |
| --- | --- | --- | --- | --- |
| design | 2026-11-22 | 0d4c7fa | CONCERNS | |

- **Search coverage:** The proposed 100,200-candidate and 64 MiB source-field limits can cause a sparse search across the existing 400,000-message test scale to return a limit error rather than matches. Trial ceilings are unmeasured. The review recommends measuring at that scale before settling the search window; accepting the limits as product behavior or changing the no-continuation policy requires an owner decision.
- **Two-stage gate:** The read-only review does not clear production implementation. If the owner accepts scratch feasibility work for T001-T003, record that scope. A new design review that covers the resulting evidence is required before T005 or later production tasks. SQLite plans, latency, heap, connection return and native behavior were not verified in this review.
