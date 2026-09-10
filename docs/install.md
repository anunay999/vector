# Install

## One-liner

```sh
curl -fsSL https://raw.githubusercontent.com/anunay999/vector/main/install.sh | sh
```

The script detects your OS/architecture, downloads the matching release, verifies
its SHA-256 checksum, and installs the `vector` binary to `~/.local/bin`. If no
release is published yet, it falls back to `go install`.

Overrides:

```sh
VECTOR_VERSION=v0.1.0 curl ... | sh      # pin a version
VECTOR_BIN_DIR=/usr/local/bin curl ... | sh
VECTOR_NO_VERIFY=1 curl ... | sh         # skip checksum verification
```

## Other methods

```sh
# Go toolchain (any platform, always current)
go install github.com/anunay999/vector/cmd/vector@latest

# From source
git clone https://github.com/anunay999/vector && cd vector
make build && ./bin/vector --help
```

## Verify

```sh
vector --version
vector doctor
```

## First run

```sh
vector init
```

`init` writes `~/.config/vector/config.yaml`, stores your OpenRouter key in the
private `~/.config/vector/env`, optionally wires your harnesses, and prints next
steps. Then:

```sh
vector up          # start the gateway in the background
vector doctor      # confirm config, keys, gateway, and wiring
```

Run it automatically at login:

```sh
vector service install     # launchd (macOS) or systemd --user (Linux)
vector service status
```

## Update

Re-run the one-liner, or `go install ...@latest`, then `vector restart` (or
`vector down && vector up`).

## Uninstall

```sh
vector service uninstall
vector down
vector claude off ; vector codex off ; vector opencode off
rm -rf ~/.config/vector
rm -f ~/.local/bin/vector
```

## Docker

For headless/CI use. Set `listen` addresses to `0.0.0.0` in the config (the
`/admin/status` endpoint stays loopback-only):

```sh
docker build -t vector .
docker run --rm -p 7331:7331 \
  -v "$HOME/.config/vector:/root/.config/vector:ro" \
  -e OPENROUTER_API_KEY=sk-or-... \
  vector
```

## CI

```yaml
- run: curl -fsSL https://raw.githubusercontent.com/anunay999/vector/main/install.sh | sh
- run: vector serve &
  env:
    OPENROUTER_API_KEY: ${{ secrets.OPENROUTER_API_KEY }}
```
