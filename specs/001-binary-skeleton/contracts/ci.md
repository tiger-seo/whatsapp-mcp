# Contract: CI workflow

This contract covers `.github/workflows/ci.yml`. The job names are the stable interface: branch protection and
later features refer to them.

| Trigger | `push` to any branch, `pull_request` |
| --- | --- |
| Permissions | `contents: read` |
| Concurrency | one run per ref; a newer push cancels the older run |
| Go version | from `go.mod` (`actions/setup-go` `go-version-file`) |
| Environment | `CGO_ENABLED=0` in every job |

| Job (as shown in GitHub) | Runner | Steps | Fails when |
| --- | --- | --- | --- |
| `check` | ubuntu-latest | `gofmt`; `go vet`; `go mod verify`; `govulncheck` (version pinned in the workflow's `GOVULNCHECK_VERSION`); `data directory denied to other users` (builds the binary, lets `status` create a data directory under `/tmp`, writes `config.toml`, and checks that `sudo -u nobody` can neither list the directory nor read the file); then one step per target, `build windows/amd64`, `build linux/amd64`, `build linux/arm64`, `build darwin/amd64` and `build darwin/arm64`, each running `go vet ./...` and `go build -o /dev/null ./cmd/whatsapp-mcp` with `GOOS`/`GOARCH` set | `gofmt -l` lists any file (the files are printed); `go vet` reports anything; the module cache does not match `go.sum`; govulncheck finds a vulnerability that the code can reach (others are only listed); user `nobody` can reach the data directory; a target does not vet or build (the failing step names the target) |
| `test (ubuntu-latest)` | ubuntu-latest | `go test ./...` | any test fails |
| `test (windows-latest)`, `test (macos-latest)` | the named OS; **only for pull requests and pushes to `main`** | `go test ./...` | any test fails on that OS |

- Every step of `check` runs even after an earlier one failed, so one run reports every problem.
- The test matrix uses `fail-fast: false`, so a failure on one OS does not hide the others.
- Pushes to other branches run `check` and `test (ubuntu-latest)` only, for quick feedback. Nothing reaches
  `main` without the tests on all three OSes, because every merge goes through a pull request.
- Changed on 2026-10-27, after the first green runs: the five separate `build (…)` jobs were merged into `check`,
  because each spent most of its time on start-up and Go setup, and the Windows and macOS tests were limited to
  pull requests and `main`.
- The workflow produces no artefacts. Release builds are feature 011 (GoReleaser).
- Every action is pinned to a full commit SHA, with its version in a comment. `.github/dependabot.yml` proposes
  updates to the actions and the Go modules weekly, as pull requests that run this workflow; a maintainer
  reviews the release notes and the new SHA before merging. `GOVULNCHECK_VERSION` is bumped by hand.
- Workflow token: `contents: read` only. The workflow runs on `pull_request`, never `pull_request_target`, and
  uses no secrets.
