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


# ensure_path adds BIN_DIR to the user's shell startup file when it is not
# already on PATH. Idempotent (marker-guarded); opt out with
# VECTOR_NO_MODIFY_PATH=1.
ensure_path() {
  case ":$PATH:" in
    *":$BIN_DIR:"*) say "ok: $BIN_DIR is already on your PATH"; return ;;
  esac

  if [ "${VECTOR_NO_MODIFY_PATH:-0}" = "1" ]; then
    path_hint
    return
  fi

  shell_name=$(basename "${SHELL:-}")
  case "$shell_name" in
    zsh)  rcs="$HOME/.zshrc"; line="export PATH=\"$BIN_DIR:\$PATH\"" ;;
    bash) rcs="$HOME/.bashrc"
          if [ -f "$HOME/.bash_profile" ]; then rcs="$HOME/.bash_profile $rcs"; fi
          line="export PATH=\"$BIN_DIR:\$PATH\"" ;;
    fish) rcs="$HOME/.config/fish/config.fish"; line="fish_add_path \"$BIN_DIR\"" ;;
    *)    path_hint; return ;;
  esac

  marker="# >>> vector >>>"
  added=""; found=0
  for rc in $rcs; do
    if [ -f "$rc" ] && grep -qF "$marker" "$rc" 2>/dev/null; then found=1; continue; fi
    mkdir -p "$(dirname "$rc")" 2>/dev/null || continue
    if printf '\n%s\n%s\n# <<< vector <<<\n' "$marker" "$line" >> "$rc" 2>/dev/null; then
      added="$added $rc"
    fi
  done

  if [ -n "$added" ]; then
    say "ok: added $BIN_DIR to PATH in:$added"
    say "    open a new shell, or run: source$(printf ' %s' $added)"
  elif [ "$found" = 1 ]; then
    say "ok: PATH already configured by vector"
  else
    path_hint
  fi
}

# path_hint prints the manual instruction when we cannot edit a startup file.
path_hint() {
  say ""
  say "Add $BIN_DIR to your PATH manually:"
  case "$(basename "${SHELL:-}")" in
    fish) say "  fish_add_path \"$BIN_DIR\"" ;;
    *)    say "  echo 'export PATH=\"$BIN_DIR:\$PATH\"' >> ~/.$(basename "${SHELL:-sh}")rc && source ~/.$(basename "${SHELL:-sh}")rc" ;;
  esac
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

ensure_path

say ""
say "Next:"
say "  vector init            # config, API key, and optional harness wiring"
say "  vector up              # start the gateway"
say "  vector doctor          # verify everything"
