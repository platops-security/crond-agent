# crond-agent

[![CI](https://github.com/platops-security/crond-agent/actions/workflows/ci.yml/badge.svg)](https://github.com/platops-security/crond-agent/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/platops-security/crond-agent.svg)](https://pkg.go.dev/github.com/platops-security/crond-agent)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

A tiny cron job monitoring agent. It **wraps a command**, runs it, and reports the
start, exit code, duration, and (optionally) output to [crond.io](https://crond.io)
or your self-hosted instance — so you know when a job fails, runs late, or stops
running entirely.

```bash
# Before:  0 3 * * *  /usr/local/sbin/backup.sh
# After:   0 3 * * *  crond-agent exec --key <ping-uuid> -- /usr/local/sbin/backup.sh
```

Single static binary, no daemon, no dependencies. ~10 MB container image.

## Install

### Hosted installer (Linux / macOS)

Downloads the latest release tarball for your platform, verifies its `checksums.txt`,
and (if `cosign` is installed) verifies the keyless-OIDC signature against this repo's
release workflow identity.

```bash
# Default: SHA256 mandatory, cosign best-effort
curl -sSfL https://get.crond.io | sh

# Strongest provenance — require a valid cosign signature (aborts otherwise)
curl -sSfL https://get.crond.io | INSTALL_REQUIRE_SIG=1 sh

# Air-gapped mirror / trusted source — skip verification (use sparingly)
curl -sSfL https://get.crond.io | INSTALL_SKIP_CHECKSUM=1 sh
```

| Env var | Default | Effect |
|---|---|---|
| `INSTALL_DIR` | `/usr/local/bin` | Destination directory; sudo used if not writable |
| `INSTALL_SKIP_CHECKSUM` | unset | Skip SHA256 **and** cosign verification entirely |
| `INSTALL_REQUIRE_SIG` | unset | Abort if cosign or signature artifacts are unavailable |

### Homebrew (macOS / Linuxbrew)

```bash
brew install platops-security/tap/crond-agent
```

### Debian / Ubuntu (`.deb`) and Fedora / RHEL (`.rpm`)

Packages are GPG-signed and attached to each [release](https://github.com/platops-security/crond-agent/releases).
There is no hosted apt/yum repository yet, so install the package directly:

```bash
# Debian / Ubuntu
VER=X.Y.Z; ARCH=amd64   # or arm64
curl -sSfLO "https://github.com/platops-security/crond-agent/releases/download/v${VER}/crond-agent_${VER}_linux_${ARCH}.deb"
sudo dpkg -i "crond-agent_${VER}_linux_${ARCH}.deb"

# Fedora / RHEL / openSUSE
VER=X.Y.Z; ARCH=x86_64  # or aarch64
sudo rpm -i "https://github.com/platops-security/crond-agent/releases/download/v${VER}/crond-agent_${VER}_linux_${ARCH}.rpm"
```

To verify package signatures, import the public signing key (published at
`https://crond.io/crond-agent-signing-key.asc`) first:

```bash
# RPM
sudo rpm --import https://crond.io/crond-agent-signing-key.asc
rpm --checksig crond-agent_${VER}_linux_${ARCH}.rpm

# DEB (requires debsig-verify configured with the key)
debsig-verify crond-agent_${VER}_linux_${ARCH}.deb
```

### Docker

```bash
docker run --rm ghcr.io/platops-security/crond-agent:latest version
```

Multi-arch (`linux/amd64`, `linux/arm64`), `FROM scratch` + CA certs, cosign-signed manifests.

### Kubernetes (Helm)

```bash
helm install crond-agent \
  oci://ghcr.io/platops-security/crond-agent/charts/crond-agent --version X.Y.Z
```

See [`charts/crond-agent/`](charts/crond-agent/) for CronJob wrapping patterns
(init-container injection and pre-baked image).

## Usage

```bash
# Wrap a command — reports start, then success/fail with exit code + duration + output
crond-agent exec --key <ping-uuid> -- /path/to/script.sh

# Hard timeout (SIGTERM, then SIGKILL after a 10s grace period)
crond-agent exec --key <ping-uuid> --timeout 30m -- /usr/bin/psql -c VACUUM

# Simple liveness ping
crond-agent ping --key <ping-uuid>

# Show the effective merged config (ping key masked)
crond-agent config

crond-agent version
```

Cron line examples are in [`scripts/cron.d.example`](scripts/cron.d.example).

## Configuration

Resolved in order (later overrides earlier): **defaults → config file → env vars → CLI flags**.

Config file search (first match wins): `/etc/crond-agent/config.yaml` →
`~/.config/crond-agent/config.yaml` → `./config.yaml`. Override with `--config`.
See [`config.example.yaml`](config.example.yaml) for every key. Each setting also has a
`CROND_*` env var (e.g. `CROND_PING_KEY`, `CROND_API_URL`, `CROND_TIMEOUT`).

### Privacy controls

The agent forwards the wrapped command's stdout/stderr to the API in the ping payload by
default. Two knobs control that:

```yaml
# /etc/crond-agent/config.yaml
capture_output: true              # default — stream output in the payload; false drops it
redact_patterns:                  # Go regexps; matches → "[REDACTED]"
  - 'Bearer [A-Za-z0-9._-]+'
  - 'postgres://[^@]+@[^/[:space:]]+'
  - '(?i)(password|api[_-]?key|secret)\s*[:=]\s*\S+'
```

Redaction is line-buffered and runs **before** the `max_output_bytes` cap, so a secret
that would straddle the truncation boundary is still caught. Patterns are validated as
regexes at startup; the agent aborts on invalid syntax. Pass-through to the host
(`kubectl logs`, `/var/log/cron`) is **not** redacted — that is the operator's local view.

#### Threat model

Protects against: routine leakage of single-line secrets (env-var dumps, DSNs, Bearer
tokens) into the API payload, including the truncation-leak case.

Does **not** protect against: a secret on a single line longer than `max_output_bytes`
with no whitespace before the boundary (raise `max_output_bytes` or set
`capture_output: false`), or multi-line secrets such as PEM blocks (use
`capture_output: false`).

## Build from source

```bash
make build          # -> bin/crond-agent
make test           # go test -race ./...
```

Requires Go 1.26+. The only runtime dependency is `gopkg.in/yaml.v3`.

## Security & supply chain

- All release artifacts ship a SHA256 `checksums.txt`, **cosign keyless-OIDC** signatures,
  and a **Syft SBOM**; binaries/packages carry **SLSA build-provenance attestations**.
- `.deb`/`.rpm` are **GPG-signed**; the container image manifests are cosign-signed.
- CI runs `gosec` (via golangci-lint) and `govulncheck`; all third-party GitHub Actions are
  pinned by commit SHA.
- See [SECURITY.md](SECURITY.md) for the disclosure process and verification details.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Licensed under [MIT](LICENSE).
