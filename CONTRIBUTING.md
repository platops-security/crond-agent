# Contributing to crond-agent

Thanks for your interest in improving crond-agent.

## Development

```bash
make build          # CGO-free static binary -> bin/crond-agent
make test           # go test -race ./...
go vet ./...
golangci-lint run   # gosec + staticcheck + errcheck + more (see .golangci.yml)
```

Requires Go 1.26+. The only runtime dependency is `gopkg.in/yaml.v3`; please keep the
dependency surface minimal.

### Before opening a PR

- `go test -race ./...` passes.
- `golangci-lint run` is clean.
- `gofmt`-formatted code (`gofmt -l .` prints nothing).
- New behavior is covered by tests; bug fixes include a regression test.
- `shellcheck install.sh scripts/postinstall.sh` is clean if you touched shell.
- If you changed `.goreleaser.yml`, run `goreleaser release --snapshot --clean --skip=sign --skip=sbom`
  (set `GPG_KEY_PATH=''`) to confirm it still builds.

## Code style

- Keep files focused and reasonably small; prefer composition over large files.
- Match the surrounding code: layered config, structured logging (`slog`), explicit error
  wrapping with `%w`.
- Comment the *why*, not the *what*, especially around signal handling and redaction.

## Commits & PRs

- Use [Conventional Commits](https://www.conventionalcommits.org/): `feat:`, `fix:`,
  `docs:`, `refactor:`, `test:`, `chore:`.
- Keep commits focused on the actual change. One logical change per PR where practical.
- CI (test, lint, govulncheck, goreleaser dry-run, helm lint, install.sh smoke) must pass.

## Reporting security issues

Do **not** open a public issue for vulnerabilities — see [SECURITY.md](SECURITY.md).
