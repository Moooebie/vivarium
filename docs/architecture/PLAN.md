# Vivarium Implementation Plan

This document captures the agreed implementation strategy derived from `DATA_MODELS.md`,
`FRONTEND.md`, and `BACKEND.md`.

## Scope Decisions

- **Language:** Go throughout (backend and, later, a Bubble Tea TUI) in a single module.
- **First milestone:** complete backend (store, keyring, Docker controller, IPC API,
  daemon lifecycle) with automated tests. No TUI yet.
- **Process model:** one `vivarium` binary that runs the daemon in-process / auto-spawns it
  and later launches the TUI. The daemon is also reachable as `vivarium --daemon`.
- **Host bridge:** implemented rootless (`internal/proxy`) with unprivileged dial ports;
  see `PROXY.md`. The TUI remains deferred.

## Repository Layout

```
vivarium2/
├── go.mod
├── cmd/vivarium/main.go         # flags, daemon spawn, (later) TUI launch
├── internal/
│   ├── models/                  # structs + validation + JSON tests
│   ├── paths/                   # XDG dirs, socket path, config.json location
│   ├── store/                   # atomic JSON persistence + seeding
│   ├── keyring/                 # SecretStore iface: libsecret + vault backends
│   ├── auth/                    # status/setup/unlock/reset lifecycle
│   ├── docker/                  # Engine API client, GPU enum, resources, images
│   │   └── dockerfiles/         # embedded standard Dockerfiles (embed.FS)
│   ├── proxy/                   # (stub) host bridge interface
│   ├── api/                     # HTTP router + handlers over unix socket + DTOs
│   └── daemon/                  # listener, single-instance lock, shutdown
└── docs/architecture/           # this document + status
```

## Phases

### Phase 0 — Scaffolding & models
- `go.mod` targeting Go 1.22+ (host has 1.27).
- `internal/models`: `BaseImage`, `APIKey`, `Recipe`, `Instance`, `GPU`, `Mount`, `Resource`,
  `Config` matching `DATA_MODELS.md` exactly.
- Validation: required fields, enums, octal `file_mode`, unique `mock_url` per recipe,
  absolute canonical paths.
- `internal/paths`: `$XDG_DATA_HOME/vivarium` (default `~/.local/share/vivarium`), socket at
  `/run/user/$UID/vivarium/vivarium.sock` with `/tmp/vivarium-$UID.sock` fallback.

### Phase 1 — Persistence (`internal/store`)
- One JSON file per entity plus `config.json`.
- Atomic writes (temp → fsync → rename), `0600` files, `0700` dirs.
- Cross-process safety via `flock`.
- Seed bundled standard images on first run.

### Phase 2 — Secret storage (`internal/keyring`) + `internal/auth`
- `SecretStore` interface: `StoreSecret/GetSecret/DeleteSecret(keyID)`.
- **libsecret backend:** D-Bus `org.freedesktop.secrets`, schema `org.vivarium.ApiKey`,
  attribute `vivarium_key_id`.
- **Vault backend:** `vault.enc` (0600); Argon2id (64 MB, t=3, p=4, 16-byte salt);
  AES-256-GCM with 96-bit nonce; `map[string]string` payload; `mlock` cached key; zeroing.
- `auth`: `GET /auth/status`, `POST /auth/setup`, `POST /auth/unlock`, reset routine.

### Phase 3 — Docker controller (`internal/docker`)
- Ensure `vivarium-net` bridge `172.28.0.0/16` gw `172.28.0.1`.
- GPU enumeration via `/sys/class/drm` + `/sys/bus/pci/devices` + `lspci`.
- Lifecycle create/start/halt/remove with mounts, env, `--add-host`, GPU passthrough flags.
- Static resource injection via in-memory tar → `PUT /containers/{id}/archive`.
- Embedded standard image builder with streamed logs.

### Phase 4 — IPC API (`internal/api`)
- `net/http` on a unix listener; JSON DTOs decoupled from models.
- All `BACKEND.md` §3 endpoints plus auth endpoints.
- Instance status reconciliation with Docker on `GET`; disk usage from Docker.
- `GET /system/status` for Docker/keyring/CA health.

### Phase 5 — Daemon lifecycle
- Flags: `--socket`, `--reset-credentials` / `--reset-vault`, internal `--daemon`.
- Single-instance lock, autospawn, stale-socket cleanup, graceful shutdown.

### Phase 6 — Frontend TUI (next milestone)
- Bubble Tea client over the unix socket implementing `FRONTEND.md` screens.

## Verification

- `go build ./...`, `go vet ./...`, `go test ./...` (unit).
- `go test -tags=integration ./...` for Docker / D-Bus (host-gated).

## Spec Notes

- `DATA_MODELS.md` says "four" bundled base images but lists two; the code seeds the two
  listed presets.
- Auth endpoints required by `FRONTEND.md` are absent from the `BACKEND.md` API list; they
  are implemented and documented here.
- `config.json` appears in `BACKEND.md` but not in `DATA_MODELS.md`; it is added to the
  storage overview.
