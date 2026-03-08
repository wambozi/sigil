#!/usr/bin/env bash
# install.sh — one-line installer for Sigil OS daemon
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/wambozi/sigil/main/scripts/install.sh | bash
#
# Supports: linux/amd64, linux/arm64

set -euo pipefail

REPO="wambozi/sigil"
INSTALL_DIR="${HOME}/.local/bin"
API_BASE="https://api.github.com/repos/${REPO}"
DOWNLOAD_BASE="https://github.com/${REPO}/releases/download"

# --- Detect OS and architecture ---------------------------------------------

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH_RAW="$(uname -m)"

case "${ARCH_RAW}" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  arm64)   ARCH="arm64" ;;
  *)
    echo "Unsupported architecture: ${ARCH_RAW}" >&2
    exit 1
    ;;
esac

if [[ "${OS}" != "linux" ]]; then
  echo "This installer supports Linux only. On macOS, build from source." >&2
  exit 1
fi

echo "Detected: ${OS}/${ARCH}"

# --- Find latest release ----------------------------------------------------

echo "Fetching latest release..."
LATEST_TAG="$(curl -fsSL "${API_BASE}/releases/latest" | python3 -c "import sys,json; print(json.load(sys.stdin)['tag_name'])")"

if [[ -z "${LATEST_TAG}" ]]; then
  echo "Failed to determine latest release tag." >&2
  exit 1
fi

echo "Latest release: ${LATEST_TAG}"

# --- Download binaries and checksums ----------------------------------------

SUFFIX="${OS}-${ARCH}"
SIGILD_BIN="sigild-${SUFFIX}"
SIGILCTL_BIN="sigilctl-${SUFFIX}"
CHECKSUM_FILE="checksums-${SUFFIX}.txt"

TMPDIR="$(mktemp -d)"
trap 'rm -rf "${TMPDIR}"' EXIT

cd "${TMPDIR}"

for FILE in "${SIGILD_BIN}" "${SIGILCTL_BIN}" "${CHECKSUM_FILE}"; do
  URL="${DOWNLOAD_BASE}/${LATEST_TAG}/${FILE}"
  echo "Downloading ${FILE}..."
  curl -fsSL -o "${FILE}" "${URL}"
done

# --- Verify SHA256 checksums ------------------------------------------------

echo "Verifying checksums..."
sha256sum --check --ignore-missing "${CHECKSUM_FILE}"
echo "Checksums OK."

# --- Install binaries -------------------------------------------------------

mkdir -p "${INSTALL_DIR}"

install -m 755 "${SIGILD_BIN}"  "${INSTALL_DIR}/sigild"
install -m 755 "${SIGILCTL_BIN}" "${INSTALL_DIR}/sigilctl"

echo "Installed to ${INSTALL_DIR}/sigild and ${INSTALL_DIR}/sigilctl"

# --- Warn if not in PATH ----------------------------------------------------

if ! echo "${PATH}" | tr ':' '\n' | grep -qx "${INSTALL_DIR}"; then
  echo ""
  echo "WARNING: ${INSTALL_DIR} is not in your PATH."
  echo "Add this to your shell rc file:"
  echo "  export PATH=\"\${HOME}/.local/bin:\${PATH}\""
fi

# --- Run sigild init -------------------------------------------------------

echo ""
echo "Running sigild init..."
"${INSTALL_DIR}/sigild" init

echo ""
echo "Installation complete. Run 'sigilctl status' to verify."
