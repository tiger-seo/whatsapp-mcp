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
| `check` | ubuntu-latest | `gofmt`; `go vet`; then one step per target, `build windows/amd64`, `build linux/amd64`, `build linux/arm64`, `build darwin/amd64` and `build darwin/arm64`, each running `go vet ./...` and `go build -o /dev/null ./cmd/whatsapp-mcp` with `GOOS`/`GOARCH` set | `gofmt -l` lists any file (the files are printed); `go vet` reports anything; a target does not vet or build (the failing step names the target) |
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
