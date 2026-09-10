#!/bin/sh
# vector installer — https://github.com/anunay999/vector
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/anunay999/vector/main/install.sh | sh
#
# Environment overrides:
#   VECTOR_VERSION   release tag to install (default: latest)
#   VECTOR_BIN_DIR   install directory (default: ~/.local/bin)
#   VECTOR_NO_VERIFY set to 1 to skip checksum verification
set -eu

REPO="anunay999/vector"
VERSION="${VECTOR_VERSION:-latest}"
BIN_DIR="${VECTOR_BIN_DIR:-$HOME/.local/bin}"
BINARY="vector"

say()  { printf '%s\n' "$*"; }
warn() { printf 'warning: %s\n' "$*" >&2; }
die()  { printf 'error: %s\n' "$*" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1; }

detect_os() {
  case "$(uname -s)" in
    Darwin) echo darwin ;;
    Linux)  echo linux ;;
    *) die "unsupported OS: $(uname -s). Install Go and run: go install github.com/$REPO/cmd/vector@latest" ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    arm64|aarch64) echo arm64 ;;
    x86_64|amd64)  echo amd64 ;;
    *) die "unsupported architecture: $(uname -m)" ;;
  esac
}

download() {
  url="$1"; out="$2"
  if need curl; then curl -fsSL "$url" -o "$out"
  elif need wget; then wget -qO "$out" "$url"
  else return 1
  fi
}

verify_checksum() {
  file="$1"; sums="$2"
  [ "${VECTOR_NO_VERIFY:-0}" = "1" ] && return 0
  base=$(basename "$file")
  expected=$(grep " $base\$" "$sums" 2>/dev/null | awk '{print $1}' | head -n1)
  [ -z "$expected" ] && { warn "no checksum for $base; skipping"; return 0; }
  if need sha256sum; then actual=$(sha256sum "$file" | awk '{print $1}')
  elif need shasum; then actual=$(shasum -a 256 "$file" | awk '{print $1}')
  else warn "no sha256 tool; skipping verification"; return 0
  fi
  [ "$expected" = "$actual" ] || die "checksum mismatch for $base"
}

install_from_release() {
  os="$1"; arch="$2"
  if [ "$VERSION" = "latest" ]; then
    base="https://github.com/$REPO/releases/latest/download"
  else
    base="https://github.com/$REPO/releases/download/$VERSION"
  fi
  asset="${BINARY}_${os}_${arch}.tar.gz"
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT

  say "downloading $asset ..."
  download "$base/$asset" "$tmp/$asset" || return 1
  download "$base/${BINARY}_checksums.txt" "$tmp/checksums.txt" || warn "checksums unavailable"
  [ -f "$tmp/checksums.txt" ] && verify_checksum "$tmp/$asset" "$tmp/checksums.txt"

  tar -xzf "$tmp/$asset" -C "$tmp"
  [ -f "$tmp/$BINARY" ] || die "archive did not contain $BINARY"
  mkdir -p "$BIN_DIR"
  install -m 0755 "$tmp/$BINARY" "$BIN_DIR/$BINARY" 2>/dev/null || {
    cp "$tmp/$BINARY" "$BIN_DIR/$BINARY" && chmod 0755 "$BIN_DIR/$BINARY"
  }
  return 0
}

install_from_go() {
  need go || return 1
  say "no release available; installing with go install ..."
  GOBIN="$BIN_DIR" go install "github.com/$REPO/cmd/vector@latest"
}

os=$(detect_os)
arch=$(detect_arch)

if ! install_from_release "$os" "$arch"; then
  warn "release download failed (no release yet?)"
  install_from_go || die "could not download a release and Go is not installed.
Install Go from https://go.dev/dl/ then run:
  go install github.com/$REPO/cmd/vector@latest"
fi

say ""
say "✓ installed $BINARY to $BIN_DIR/$BINARY"

case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) say ""
     say "Add $BIN_DIR to your PATH:"
     say "  echo 'export PATH=\"$BIN_DIR:\$PATH\"' >> ~/.zshrc && source ~/.zshrc" ;;
esac

say ""
say "Next:"
say "  vector init            # config, API key, and optional harness wiring"
say "  vector up              # start the gateway"
say "  vector doctor          # verify everything"
