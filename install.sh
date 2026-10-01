#!/bin/sh
# Install uvpm:  curl -fsSL https://raw.githubusercontent.com/giapnguyen74/uvpm/main/install.sh | sh
# Env: UVPM_VERSION (default: latest release), UVPM_INSTALL_DIR (default: ~/.local/bin)
set -eu

REPO="giapnguyen74/uvpm"
DIR="${UVPM_INSTALL_DIR:-$HOME/.local/bin}"

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) echo "uvpm: unsupported OS $(uname -s)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) echo "uvpm: unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac

asset="uvpm-$os-$arch"
if [ -n "${UVPM_VERSION:-}" ]; then
  base="https://github.com/$REPO/releases/download/$UVPM_VERSION"
else
  base="https://github.com/$REPO/releases/latest/download"
fi

if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL "$1" -o "$2"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -qO "$2" "$1"; }
else
  echo "uvpm: curl or wget is required" >&2; exit 1
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "Downloading $asset ..."
fetch "$base/$asset" "$tmp/uvpm"

# Verify the checksum when the release publishes one.
if fetch "$base/checksums.txt" "$tmp/checksums.txt" 2>/dev/null; then
  want="$(grep " $asset\$" "$tmp/checksums.txt" | cut -d' ' -f1)"
  if command -v sha256sum >/dev/null 2>&1; then got="$(sha256sum "$tmp/uvpm" | cut -d' ' -f1)"
  else got="$(shasum -a 256 "$tmp/uvpm" | cut -d' ' -f1)"; fi
  if [ -n "$want" ] && [ "$want" != "$got" ]; then
    echo "uvpm: checksum mismatch" >&2; exit 1
  fi
fi

mkdir -p "$DIR"
chmod +x "$tmp/uvpm"
mv "$tmp/uvpm" "$DIR/uvpm"
echo "Installed uvpm to $DIR/uvpm"

case ":$PATH:" in
  *":$DIR:"*) ;;
  *) echo "Note: $DIR is not on your PATH. Add: export PATH=\"$DIR:\$PATH\"" ;;
esac
command -v uv >/dev/null 2>&1 || echo "Note: uv not found; uvpm needs it for uv projects (https://docs.astral.sh/uv/)."
