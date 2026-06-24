#!/bin/sh
# crond-agent installer — downloads the latest release for your platform.
# Usage: curl -sSfL https://get.crond.io | sh
set -e

REPO="platops-security/crond-agent"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

# Detect OS and architecture.
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

case "$OS" in
  linux|darwin|freebsd) ;;
  *) echo "Unsupported OS: $OS"; exit 1 ;;
esac

echo "Detected platform: ${OS}/${ARCH}"

# Resolve the latest STABLE agent release version (MAJOR.MINOR.PATCH).
# We list releases and keep only exact agent semver tags ("vX.Y.Z"), then take
# the newest. This deliberately skips `chart-v*` Helm releases, the rolling
# `nightly` pre-release, and `-rc`/`-beta` prereleases. Using /releases/latest
# instead would break here: it returns whichever non-prerelease release is
# newest, which can be a `chart-v*` tag and yields a malformed download URL.
LATEST=$(curl -sSf "https://api.github.com/repos/${REPO}/releases" \
  | grep -oE '"tag_name": *"v[0-9]+\.[0-9]+\.[0-9]+"' \
  | sed -E 's/.*"v([0-9]+\.[0-9]+\.[0-9]+)"/\1/' \
  | head -n1)
if [ -z "$LATEST" ]; then
  echo "Failed to determine latest version"; exit 1
fi
echo "Latest version: v${LATEST}"

# Download and extract.
TARBALL="crond-agent_${LATEST}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/v${LATEST}/${TARBALL}"

echo "Downloading ${URL}..."
TMP=$(mktemp -d)
curl -sSfL "$URL" -o "${TMP}/${TARBALL}"

# Verify checksum. Mandatory by default — supply-chain protection for
# `curl ... | sh`. Override with INSTALL_SKIP_CHECKSUM=1 only if you know
# what you're doing (e.g. air-gapped mirror you trust).
CHECKSUM_URL="https://github.com/${REPO}/releases/download/v${LATEST}/checksums.txt"
if [ "${INSTALL_SKIP_CHECKSUM:-}" = "1" ]; then
  echo "WARNING: INSTALL_SKIP_CHECKSUM=1 — skipping checksum verification."
else
  if ! curl -sSfL "$CHECKSUM_URL" -o "${TMP}/checksums.txt"; then
    echo "ERROR: failed to fetch ${CHECKSUM_URL} — aborting install." >&2
    echo "       (set INSTALL_SKIP_CHECKSUM=1 to bypass at your own risk)" >&2
    exit 1
  fi
  cd "$TMP"
  # Select the checksum line by an exact filename match on field 2 — not a
  # substring grep, which would also match e.g. "${TARBALL}.sig"/".pem" lines
  # and feed extra/blank lines to the verifier. awk's END exit aborts on a
  # zero-match instead of silently verifying nothing (no pipefail needed, which
  # POSIX /bin/sh lacks).
  EXPECTED=$(awk -v f="$TARBALL" '$2 == f { print; found = 1 } END { exit !found }' checksums.txt) || {
    echo "ERROR: no checksum entry for ${TARBALL} in checksums.txt — aborting." >&2
    exit 1
  }
  if command -v sha256sum >/dev/null 2>&1; then
    printf '%s\n' "$EXPECTED" | sha256sum -c --quiet
  elif command -v shasum >/dev/null 2>&1; then
    printf '%s\n' "$EXPECTED" | shasum -a 256 -c --quiet
  else
    echo "ERROR: neither sha256sum nor shasum found — cannot verify download." >&2
    echo "       Install coreutils (or use INSTALL_SKIP_CHECKSUM=1 at your own risk)." >&2
    exit 1
  fi
  cd - >/dev/null
  echo "Checksum verified."
fi

# Cosign signature verification — strongest provenance check. Pins the
# accepted signer to the release workflow's OIDC identity (keyless), so a
# leaked GH token alone can't forge a valid signature; an attacker would
# need to run this exact workflow on this repo.
#
# Best-effort by default (skip if cosign missing). Set INSTALL_REQUIRE_SIG=1
# to make verification mandatory in security-critical environments.
# INSTALL_REQUIRE_SIG=1 + INSTALL_SKIP_CHECKSUM=1 is a contradiction and
# aborts up front.
if [ "${INSTALL_REQUIRE_SIG:-}" = "1" ] && [ "${INSTALL_SKIP_CHECKSUM:-}" = "1" ]; then
  echo "ERROR: INSTALL_REQUIRE_SIG=1 and INSTALL_SKIP_CHECKSUM=1 are mutually exclusive." >&2
  exit 1
fi
SIG_URL="https://github.com/${REPO}/releases/download/v${LATEST}/checksums.txt.sig"
CERT_URL="https://github.com/${REPO}/releases/download/v${LATEST}/checksums.txt.pem"
COSIGN_IDENTITY="https://github.com/${REPO}/.github/workflows/release.yml@refs/tags/v${LATEST}"
COSIGN_ISSUER="https://token.actions.githubusercontent.com"
if [ "${INSTALL_SKIP_CHECKSUM:-}" != "1" ]; then
  if command -v cosign >/dev/null 2>&1; then
    if curl -sSfL "$SIG_URL" -o "${TMP}/checksums.txt.sig" \
      && curl -sSfL "$CERT_URL" -o "${TMP}/checksums.txt.pem"; then
      cosign verify-blob \
        --certificate "${TMP}/checksums.txt.pem" \
        --signature "${TMP}/checksums.txt.sig" \
        --certificate-identity "$COSIGN_IDENTITY" \
        --certificate-oidc-issuer "$COSIGN_ISSUER" \
        "${TMP}/checksums.txt"
      echo "Cosign signature verified (identity=${COSIGN_IDENTITY})."
    else
      echo "WARNING: cosign signature artifacts not found for v${LATEST} — checksum-only verification." >&2
      if [ "${INSTALL_REQUIRE_SIG:-}" = "1" ]; then
        echo "ERROR: INSTALL_REQUIRE_SIG=1 set and signature artifacts missing — aborting." >&2
        exit 1
      fi
    fi
  else
    echo "Note: cosign not installed — skipping signature verification. Install cosign for stronger provenance checks." >&2
    if [ "${INSTALL_REQUIRE_SIG:-}" = "1" ]; then
      echo "ERROR: INSTALL_REQUIRE_SIG=1 set and cosign is not available — aborting." >&2
      exit 1
    fi
  fi
fi

tar -xzf "${TMP}/${TARBALL}" -C "$TMP"

# Install binary.
if [ -w "$INSTALL_DIR" ]; then
  cp "${TMP}/crond-agent" "${INSTALL_DIR}/crond-agent"
else
  echo "Installing to ${INSTALL_DIR} (may require sudo)..."
  sudo cp "${TMP}/crond-agent" "${INSTALL_DIR}/crond-agent"
fi
chmod +x "${INSTALL_DIR}/crond-agent"

rm -rf "$TMP"

echo "crond-agent installed to ${INSTALL_DIR}/crond-agent"
"${INSTALL_DIR}/crond-agent" version
