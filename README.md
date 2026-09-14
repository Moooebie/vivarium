# Vivarium

Vivarium provisions isolated Docker sandboxes for AI coding agents. It keeps
upstream API keys off the agent's filesystem, injecting them through a local
TLS-intercepting host bridge, and exposes everything through a keyboard-driven
terminal UI.

- **Backend daemon** — storage, encrypted secrets, Docker lifecycle, host bridge
  proxy, Unix-socket JSON API (`BACKEND.md`).
- **TUI frontend** — Bubble Tea interface for instances, recipes, API keys, and
  base images (`FRONTEND.md`).
- **Data model** — see `DATA_MODELS.md`.

Both tiers ship in one binary. `vivarium` launches the TUI and connects to a
running backend, starting a **detached** one if none is present so several
terminals can share it. `--daemon` runs the backend headless in the foreground,
`--stop` stops a running backend.

## Requirements

- Linux (x86_64) with Docker Engine
- Go 1.24+ (module targets 1.26)
- Optional: AMD ROCm/DRI drivers for GPU passthrough
- Optional: a running D-Bus Secret Service for the `libsecret` backend

## Build

```bash
make build            # builds the guest relay, then -> ./vivarium
# or
go build -o vivarium ./cmd/vivarium
```

`make build` first rebuilds the embedded guest relay
(`cmd/vivarium-guestbridge` → `internal/guestbridge/bin/`), which is committed
so a plain `go build`/`go test` works without it.

## Run

```bash
./vivarium                      # TUI (auto-starts a detached backend if needed)
./vivarium --daemon             # backend only, foreground
./vivarium --stop               # stop a running backend
./vivarium --version
./vivarium --reset-credentials  # wipe keys and reset encryption (--yes to skip prompt)
```

On first launch, choose **System Keyring** or a **Local Encrypted Vault** and set
a master password. The host bridge binds the `vivarium-net` gateway on
`172.28.0.1:8443` (TLS) and `:8080` (plain). With the vault backend the daemon
re-locks after 15 minutes of inactivity (`--idle-lock`).

### Flags

| Flag | Default | Description |
| --- | --- | --- |
| `--socket PATH` | XDG runtime path | Override the backend Unix socket. |
| `--daemon` | `false` | Run the backend without the TUI. |
| `--stop` | `false` | Stop a running backend daemon and exit. |
| `--idle-lock DUR` | `15m` | Lock the vault after this much inactivity (`0` disables). |
| `--version` | | Print the version and exit. |
| `--reset-credentials`, `--reset-vault` | | Delete API keys and reset encryption. |
| `--yes` | `false` | Skip confirmation prompts. |
| `--proxy-port N` | `8443` | Host bridge TLS port. |
| `--proxy-http-port N` | `8080` | Host bridge plain HTTP port. |
| `--no-proxy` | `false` | Disable the host bridge. |

## Test

```bash
make test              # unit tests
make race              # unit tests with the race detector
make vet               # go vet (including integration-tagged files)

# integration tests: real Docker (network, GPU, TLS MITM proxy), Secret Service,
# and a full agent e2e (skipped without a key)
make test-integration
# or
VIVARIUM_INTEGRATION=1 go test -tags=integration ./internal/docker/ ./internal/proxy/ ./internal/keyring/ ./internal/e2e/
```

## Layout

```
cmd/vivarium/        entrypoint: mode dispatch (TUI | --daemon | --stop | reset)
internal/
  models/            persisted entities + validation
  store/             atomic JSON persistence
  keyring/           libsecret + encrypted vault secret stores
  auth/              setup / unlock / reset lifecycle
  docker/            Docker Engine API client and controller
  proxy/             TLS-intercepting host bridge
  api/               Unix-socket JSON API
  apitypes/          shared wire types
  client/            typed API client used by the TUI
  tui/               Bubble Tea frontend
  daemon/            socket listener + lifecycle
docs/architecture/   design notes (PLAN, STATUS, PROXY, FRONTEND_PLAN)
```

## Documentation

- `docs/architecture/STATUS.md` — what is implemented and deferred
- `docs/architecture/PROXY.md` — host bridge design
- `docs/architecture/FRONTEND_PLAN.md` — TUI architecture
