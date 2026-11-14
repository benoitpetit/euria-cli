#!/usr/bin/env sh
# Euria Retro — quick installer (Linux / macOS)
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/benoitpetit/euria_retro/main/install.sh | sh
#   curl -fsSL .../install.sh | sh -s -- --dir /usr/local/bin
set -eu

REPO="benoitpetit/euria_retro"
BINDIR="${HOME}/.local/bin"
VERSION="latest"

while [ $# -gt 0 ]; do
    case "$1" in
        --dir) BINDIR="$2"; shift 2 ;;
        --version) VERSION="$2"; shift 2 ;;
        -h|--help)
            cat <<EOF
Usage: install.sh [--dir DIR] [--version TAG]
  --dir DIR       Installation directory (default: \$HOME/.local/bin)
  --version TAG   Release tag (default: latest)
EOF
            exit 0 ;;
        *) echo "Unknown option: $1" >&2; exit 2 ;;
    esac
done

os="$(uname -s)"
arch="$(uname -m)"
case "$os" in
    Linux)  os_part="linux" ;;
    Darwin) os_part="darwin" ;;
    *) echo "Unsupported OS: $os" >&2; exit 1 ;;
esac
case "$arch" in
    x86_64|amd64) arch_part="amd64" ;;
    aarch64|arm64) arch_part="arm64" ;;
    armv7l|armv7) arch_part="armv7" ;;
    *) echo "Unsupported architecture: $arch" >&2; exit 1 ;;
esac

if [ "$os_part" = "linux" ] && [ "$arch_part" = "armv7" ]; then
    asset="euria_linux_armv7"
else
    asset="euria_${os_part}_${arch_part}"
fi
if [ "$os_part" = "darwin" ]; then
    asset="${asset}"
fi

base_url="https://github.com/${REPO}/releases"
if [ "$VERSION" = "latest" ]; then
    url="${base_url}/latest/download/${asset}"
    sums_url="${base_url}/latest/download/SHA256SUMS"
else
    url="${base_url}/download/${VERSION}/${asset}"
    sums_url="${base_url}/download/${VERSION}/SHA256SUMS"
fi

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

echo "Downloading ${asset} (${VERSION})..."
curl -fL "$url" -o "${tmpdir}/${asset}"
curl -fL "$sums_url" -o "${tmpdir}/SHA256SUMS"

cd "$tmpdir"
if command -v sha256sum >/dev/null 2>&1; then
    sha256sum -c SHA256SUMS --ignore-missing >/dev/null 2>&1 || {
        echo "Checksum verification failed." >&2; exit 1; }
elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 -c SHA256SUMS 2>/dev/null | grep -q "${asset}: OK" || {
        echo "Checksum verification failed." >&2; exit 1; }
else
    echo "Warning: no sha256 tool found; skipping checksum verification." >&2
fi

mkdir -p "$BINDIR"
install -m 0755 "${tmpdir}/${asset}" "${BINDIR}/euria"

echo "Installed euria to ${BINDIR}/euria"
case ":${PATH}:" in
    *":${BINDIR}:"*) ;;
    *) echo "Add ${BINDIR} to your PATH to use 'euria'." ;;
esac
"${BINDIR}/euria" --version