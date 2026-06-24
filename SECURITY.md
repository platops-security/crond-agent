# Security Policy

## Reporting a vulnerability

Please report security issues **privately**. Do not open a public GitHub issue.

- Preferred: open a [private security advisory](https://github.com/platops-security/crond-agent/security/advisories/new).
- Or email **security@crond.io** with details and, if possible, a proof of concept.

We aim to acknowledge within 3 business days and to ship a fix or mitigation as quickly as
the severity warrants. Please give us a reasonable window to remediate before any public
disclosure.

## Supported versions

Security fixes target the latest released minor version. Older versions are best-effort.

## Verifying releases

Every tagged release publishes signed, attested artifacts.

**Checksums + cosign (keyless OIDC):**

```bash
# checksums.txt is signed by this repo's release workflow identity
cosign verify-blob \
  --certificate checksums.txt.pem \
  --signature checksums.txt.sig \
  --certificate-identity "https://github.com/platops-security/crond-agent/.github/workflows/release.yml@refs/tags/vX.Y.Z" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  checksums.txt
```

**Container image (cosign):**

```bash
cosign verify \
  --certificate-identity-regexp "https://github.com/platops-security/crond-agent/.github/workflows/release.yml@.*" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  ghcr.io/platops-security/crond-agent:vX.Y.Z
```

**SLSA build provenance:**

```bash
gh attestation verify <artifact> --repo platops-security/crond-agent
```

**`.deb` / `.rpm` GPG signatures:** import the public key from
`https://crond.io/crond-agent-signing-key.asc` and verify with `rpm --checksig` /
`debsig-verify` (see the README install section).

## Security posture

- Single static binary, `CGO_ENABLED=0`, minimal dependency surface (`gopkg.in/yaml.v3`).
- CI runs `gosec` (via golangci-lint) and `govulncheck` on every change.
- All third-party GitHub Actions are pinned by commit SHA; the base container image is
  pinned by digest.
- Secrets (ping keys) are masked in logs and in `crond-agent config` output. TLS verification
  is on by default; disabling it prints a loud warning.
