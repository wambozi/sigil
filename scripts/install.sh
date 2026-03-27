#!/bin/sh
# install.sh — universal installer for Sigil (sigild + sigilctl + plugins)
#
# Usage:
#   curl -fsSL https://get.sigil.dev | sh
#   curl -fsSL https://get.sigil.dev | sh -s -- --help
#   curl -fsSL https://get.sigil.dev | sh -s -- --uninstall
#
# Supports: linux/amd64, linux/arm64, darwin/amd64, darwin/arm64
# Requires: curl (or wget), sha256sum (or shasum)
# Does NOT require root/sudo.

set -eu

if [ -z "${HOME:-}" ]; then
  printf 'Error: %s\n' "HOME is not set; cannot determine install paths" >&2
  exit 1
fi

REPO="sigil-tech/sigil"
API_BASE="https://api.github.com/repos/${REPO}"
DOWNLOAD_BASE="https://github.com/${REPO}/releases/download"
INSTALL_DIR="${PREFIX:-${HOME}/.local/bin}"
CONFIG_DIR="${XDG_CONFIG_HOME:-${HOME}/.config}/sigil"

CORE_BINS="sigild sigilctl"
PLUGIN_BINS="sigil-plugin-claude sigil-plugin-github sigil-plugin-jira sigil-plugin-vscode"
ALL_BINS="${CORE_BINS}"

# --- Helpers ------------------------------------------------------------------

log() {
  printf '%s\n' "$@"
}

err() {
  printf 'Error: %s\n' "$1" >&2
  exit 1
}

usage() {
  cat <<'USAGE'
Usage: install.sh [OPTIONS]

Install sigild and sigilctl from GitHub Releases.

Options:
  --help           Show this help message and exit
  --with-plugins   Also install plugin binaries (claude, github, jira, vscode)
  --uninstall      Remove installed binaries and config directory

Environment:
  PREFIX              Install directory (default: $HOME/.local/bin)
  XDG_CONFIG_HOME     Config base directory (default: $HOME/.config)

Examples:
  curl -fsSL https://get.sigil.dev | sh
  PREFIX=/usr/local/bin sh install.sh
  sh install.sh --uninstall
USAGE
  exit 0
}

# Portable HTTP GET to stdout. Prefers curl, falls back to wget.
fetch() {
  if command -v curl >/dev/null 2>&1; then
    curl --proto '=https' --tlsv1.2 -fsSL "$1"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO- "$1" || err "download failed: $1"
  else
    err "curl or wget is required"
  fi
}

# Portable HTTP GET to file. Prefers curl, falls back to wget.
fetch_to() {
  if command -v curl >/dev/null 2>&1; then
    curl --proto '=https' --tlsv1.2 -fsSL -o "$2" "$1"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$2" "$1" || err "download failed: $1"
  else
    err "curl or wget is required"
  fi
}

# Portable SHA256 verification. Prefers sha256sum, falls back to shasum.
# Usage: verify_sha256 <checksum_file> <bin1> <bin2> ...
verify_sha256() {
  checksum_file="$1"
  shift
  # Filter checksum file to only include entries for downloaded binaries
  filtered="${checksum_file}.filtered"
  : > "${filtered}"
  for bin in "$@"; do
    grep "  ${bin}\$" "${checksum_file}" >> "${filtered}" 2>/dev/null || \
      grep " ${bin}\$" "${checksum_file}" >> "${filtered}" 2>/dev/null || \
      err "no checksum found for ${bin}"
  done
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum --check "${filtered}"
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 --check "${filtered}"
  else
    err "sha256sum or shasum is required for checksum verification"
  fi
}

# --- Parse flags --------------------------------------------------------------

action="install"

for arg in "$@"; do
  case "${arg}" in
    --help)         usage ;;
    --with-plugins) ALL_BINS="${CORE_BINS} ${PLUGIN_BINS}" ;;
    --uninstall)    action="uninstall" ;;
    *)              err "unknown option: ${arg}" ;;
  esac
done

# --- Uninstall ----------------------------------------------------------------

if [ "${action}" = "uninstall" ]; then
  log "Uninstalling Sigil from ${INSTALL_DIR}..."
  removed=0
  for bin in ${ALL_BINS}; do
    target="${INSTALL_DIR}/${bin}"
    if [ -f "${target}" ]; then
      rm -f "${target}"
      log "  removed ${target}"
      removed=$((removed + 1))
    fi
  done
  if [ -d "${CONFIG_DIR}" ]; then
    rm -rf "${CONFIG_DIR}"
    log "  removed ${CONFIG_DIR}"
    removed=$((removed + 1))
  fi
  if [ "${removed}" -eq 0 ]; then
    log "Nothing to remove."
  else
    log "Uninstall complete."
  fi
  exit 0
fi

# --- Detect OS ----------------------------------------------------------------

raw_os="$(uname -s)"
case "${raw_os}" in
  Linux)  os="linux" ;;
  Darwin) os="darwin" ;;
  *)      err "unsupported OS: ${raw_os}" ;;
esac

# --- Detect architecture ------------------------------------------------------

raw_arch="$(uname -m)"
case "${raw_arch}" in
  x86_64)  arch="amd64" ;;
  aarch64) arch="arm64" ;;
  arm64)   arch="arm64" ;;
  *)       err "unsupported architecture: ${raw_arch}" ;;
esac

log "Detected: ${os}/${arch}"

# --- Resolve latest version ---------------------------------------------------

log "Fetching latest release..."
latest_tag="$(fetch "${API_BASE}/releases/latest" \
  | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' \
  | head -n 1)"

if [ -z "${latest_tag}" ]; then
  err "failed to determine latest release tag"
fi

log "Latest release: ${latest_tag}"

# --- Download binaries and checksums ------------------------------------------

suffix="${os}-${arch}"
checksum_file="checksums-${suffix}.txt"

tmpdir="$(mktemp -d)"
trap 'rm -rf "${tmpdir}"' EXIT

# Download checksum file first
log "Downloading ${checksum_file}..."
fetch_to "${DOWNLOAD_BASE}/${latest_tag}/${checksum_file}" "${tmpdir}/${checksum_file}"

# Download all binaries
downloaded_bins=""
for bin in ${ALL_BINS}; do
  remote_name="${bin}-${suffix}"
  url="${DOWNLOAD_BASE}/${latest_tag}/${remote_name}"
  log "Downloading ${remote_name}..."
  fetch_to "${url}" "${tmpdir}/${remote_name}"
  downloaded_bins="${downloaded_bins} ${remote_name}"
done

# --- Verify SHA256 checksums -------------------------------------------------

log "Verifying checksums..."
# shellcheck disable=SC2086
( cd "${tmpdir}" && verify_sha256 "${checksum_file}" ${downloaded_bins} )
log "Checksums OK."

# --- Install ------------------------------------------------------------------

mkdir -p "${INSTALL_DIR}"

for bin in ${ALL_BINS}; do
  remote_name="${bin}-${suffix}"
  install -m 755 "${tmpdir}/${remote_name}" "${INSTALL_DIR}/${bin}"
done

log ""
log "Installed to ${INSTALL_DIR}:"
for bin in ${ALL_BINS}; do
  log "  ${INSTALL_DIR}/${bin}"
done

# --- Create config directory --------------------------------------------------

mkdir -p "${CONFIG_DIR}"
log ""
log "Config directory: ${CONFIG_DIR}"

# --- PATH advice --------------------------------------------------------------

case ":${PATH}:" in
  *":${INSTALL_DIR}:"*) ;;
  *)
    log ""
    log "WARNING: ${INSTALL_DIR} is not in your PATH."
    log "Add this to your shell profile:"
    log "  export PATH=\"\${HOME}/.local/bin:\${PATH}\""
    ;;
esac

# --- Next steps ---------------------------------------------------------------

# NOTE: Previous versions auto-ran 'sigild init' after install.
# This was removed to keep the installer non-interactive.
# Users should run 'sigild init' manually to complete setup.

log ""
log "Run 'sigild init' to complete setup."
