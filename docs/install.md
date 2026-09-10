# Install

## One-liner

```sh
curl -fsSL https://raw.githubusercontent.com/anunay999/vector/main/install.sh | sh
```

The script detects your OS/architecture, downloads the matching release, verifies
its SHA-256 checksum, and installs the `vector` binary to `~/.local/bin`. If no
release is published yet, it falls back to `go install`.

It also makes `vector` findable: if the install directory is not already on your
`PATH`, the script appends a marker-guarded block to your shell startup file
(`~/.zshrc`, `~/.bashrc`/`~/.bash_profile`, or fish's `config.fish`). Re-running
the installer is safe — the block is only added once. No manual `PATH` edit and no
shell restart are needed; open a new shell (or `source` the file) and `vector`
resolves.

Overrides:

```sh
VECTOR_VERSION=v0.1.0 curl ... | sh          # pin a version
VECTOR_BIN_DIR=/usr/local/bin curl ... | sh  # install elsewhere (already on PATH)
VECTOR_NO_MODIFY_PATH=1 curl ... | sh        # don't touch shell startup files
VECTOR_NO_VERIFY=1 curl ... | sh             # skip checksum verification
```

If `VECTOR_NO_MODIFY_PATH=1` is set (or the shell can't be detected) the script
prints the line to add yourself. Note that `go install` without `GOBIN` puts the
binary in `$(go env GOPATH)/bin`, which is often not on `PATH`; use the one-liner
or `vector` will not be found.

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
