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
| `check` | ubuntu-latest | `gofmt -l .`; `go vet ./...` | `gofmt -l` lists any file (the files are printed); `go vet` reports anything |
| `test (ubuntu-latest)`, `test (windows-latest)`, `test (macos-latest)` | the named OS | `go test ./...` | any test fails on that OS |
| `build (windows/amd64)`, `build (linux/amd64)`, `build (linux/arm64)`, `build (darwin/amd64)`, `build (darwin/arm64)` | ubuntu-latest | `go vet ./...` and `go build -o /dev/null ./cmd/whatsapp-mcp` with `GOOS`/`GOARCH` set | the target does not vet or build |

- Every matrix uses `fail-fast: false`, so a failure on one OS or target does not hide the others.
- The workflow produces no artefacts. Release builds are feature 011 (GoReleaser).
