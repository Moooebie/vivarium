# Vivarium Status

The backend (including the host-bridge reverse proxy) and a Bubble Tea TUI
frontend are implemented and tested.

## Implemented

| Area | Package | Notes |
| --- | --- | --- |
| Models & validation | `internal/models` | All entities from `DATA_MODELS.md`; unique `mock_url` per recipe; per-instance endpoint bindings. |
| Path resolution | `internal/paths` | XDG data/state dirs, socket path, `/tmp` fallback, daemon log/PID paths. |
| Persistence | `internal/store` | Atomic writes, `0600` files / `0700` dirs, preset seeding. |
| Secret storage | `internal/keyring` | `SecretStore` interface, local Argon2id + AES-256-GCM vault, libsecret over D-Bus. |
| Auth lifecycle | `internal/auth` | `unconfigured` / `locked` / `unlocked`, setup, unlock, reset, idle auto-lock. |
| Docker | `internal/docker` | Direct Engine API client, `vivarium-net`, GPU enum, mounts, `--add-host`, resource/file injection, detached exec, interactive exec sessions (connect), CA trust injection, guest-relay lifecycle, standard image builds. |
| Host bridge | `internal/proxy` | Rootless TLS MITM, CA + leaf minting, endpoint registry, dummy-token validation, credential injection, rate limiting, SSE streaming. |
| Guest relay | `cmd/vivarium-guestbridge` | Static TCP forwarder hijacking agent traffic (`127.0.0.1:443`/`:80`) to the unprivileged host bridge; embedded via `internal/guestbridge`. |
| IPC API | `internal/api` | Full `BACKEND.md` §3 surface plus auth endpoints, `ping`, instance `PUT`, secret reveal, and per-session exec connect/resize. |
| Wire types | `internal/apitypes` | JSON DTOs shared by the API and the client. |
| API client | `internal/client` | Typed Unix-socket client with exec connect upgrade. |
| TUI | `internal/tui` | Bubble Tea frontend: auth, Main, Instances, Wizard, Edit, Recipes, API Keys, Base Images, Connection Info, connect shell. Unified vertical row forms; recipe editor drill-down pages. |
| Daemon | `internal/daemon` | Unix socket listener, `flock` single-instance lock, PID file, bridge lifecycle, graceful shutdown. |
| CLI | `cmd/vivarium` | Default TUI with detached auto-spawned backend; `--daemon`, `--stop`, `--idle-lock`, `--version`, `--socket`, reset flags, proxy flags. |

See `PROXY.md` for the bridge design and `FRONTEND_PLAN.md` for the TUI.

## Deferred / Follow-ups

- Optional: mock D-Bus Secret Service test harness (`dbusmock`).

The frontend completion work (M6) is done: nav/edit fields, clickable
dual-control buttons, working indicator, discard prompts, mouse
click/double-click, full 5-step wizard with an ephemeral customizer, recipe
sub-editors, and base-image status/size. See `FRONTEND_PLAN.md`.

## Design Decisions

- **No Docker SDK.** The Engine REST API is called directly over
  `/var/run/docker.sock`; this keeps the dependency set tiny (godbus, x/crypto,
  x/sys, uuid) and maps 1:1 to the operations in the spec.
- **libsecret over raw D-Bus.** An off-the-shelf helper cannot express the
  required `org.vivarium.ApiKey` schema and `vivarium_key_id` attribute, so the
  Secret Service calls are implemented directly with `godbus/v5`.
- **Persistent keyring collections only.** Items are written to the persistent
  `login`/`default` collection, never the ephemeral `session` collection (whose
  items vanish when the keyring session ends). If only the session collection
  exists, storage fails with a clear error instead of losing secrets.
- **Marker attribute + key-id lookup.** Created items carry a stable
  `vivarium=1` marker and are looked up by `vivarium_key_id` alone, so lookups
  do not depend on the service preserving a custom `xdg:schema`.
- **Verify after store.** `StoreSecret` reads the value back and fails if it is
  not retrievable, so API-key metadata is never persisted for an unrecoverable
  secret.
- **`config.json` gains a `configured` flag.** The spec does not define how a
  fresh install is distinguished from a configured one; this explicit flag
  drives the `unconfigured` status.
- **Instance env tokens.** Dummy `viv-tok-*` tokens and provider base URLs are
  generated per instance and persisted as `InstanceEndpoint` records so the
  bridge can resolve `mock_url → base_url + keyID`; only dummy tokens are
  stored, never real secrets.
- **Transparent DNS hijack + guest relay.** The host proxy stays on the
  unprivileged gateway ports (`:8443`/`:8080`). Each container gets
  `--add-host <mockhost>:127.0.0.1` and a small root-started TCP relay
  (`127.0.0.1:443`/`:80` → gateway) so agents use their **default** base URLs.
  No host root and no `iptables`. See `PROXY.md`.
- **No base-URL injection.** Vivarium injects only the dummy provider API-key
  variable for standard providers; agents use their built-in defaults. Custom
  endpoints are configured by the user (Vivarium only makes `mock_url` reachable
  and proxied). The earlier OpenCode-config workaround is removed.
- **Relay lifecycle.** The relay is injected `0755`, started detached as root
  after the container starts, readiness-checked, and re-started when a halted
  instance starts; failure aborts instance creation.
- **Numeric GPU group IDs.** `--group-add` resolves names against the
  *container's* `/etc/group`, which often lacks `render`; the controller
  therefore passes numeric GIDs resolved from the host (`video`/`render` and
  the device-node owning groups), which is portable across images.
- **Connect = exec, not attach.** Interactive shells use Docker's exec API
  (`/containers/{id}/exec` + `/exec/{id}/start` upgrade), so every client gets
  an independent shell. Container attach (PID 1 stdio) would mirror sessions.
  The exec ID is returned in the `X-Vivarium-Exec` header for per-session
  resizes. The shell is `bash -l` when present, else `sh`.
- **Detached backend.** The TUI connects to an existing daemon or spawns one
  with `setsid` (logging to `$XDG_STATE_HOME/vivarium/daemon.log`) so it
  outlives the TUI and can be shared by multiple terminals. The daemon writes a
  PID file for `--stop`. An in-process fallback remains if spawning fails.
- **Idle auto-lock.** With the vault backend the daemon locks the vault after
  `--idle-lock` (default 15m) without secret access; libsecret is owned by the
  desktop keyring and is unaffected.

## API Surface

All routes are prefixed with `/api/v1`.

```
GET    /auth/status
POST   /auth/setup
POST   /auth/unlock

GET    /base-images
POST   /base-images
DELETE /base-images/{id}
POST   /base-images/rebuild            # streams text/plain build logs

GET    /api-keys
POST   /api-keys
PUT    /api-keys/{id}
DELETE /api-keys/{id}
GET    /api-keys/{id}/secret
POST   /api-keys/test                  # test an unsaved payload (optional key_id)
POST   /api-keys/{id}/test

GET    /recipes
POST   /recipes
GET    /recipes/{id}
PUT    /recipes/{id}
DELETE /recipes/{id}

GET    /instances
POST   /instances
GET    /instances/{id}
POST   /instances/{id}/start
POST   /instances/{id}/halt
DELETE /instances/{id}
POST   /instances/{id}/inject
GET    /instances/{id}/connect         # 101 Switching Protocols; new exec shell
POST   /instances/{id}/exec/{execID}/resize

GET    /system/gpus
GET    /system/status
GET    /ping
```

## Verification

```
go build ./...
go vet ./...
go test ./...
go test -race ./internal/...
VIVARIUM_INTEGRATION=1 go test -tags=integration ./internal/docker/
```

The Docker integration tests provision real containers on `vivarium-net`; the
bridge test drives an agent through the proxy to a host mock upstream and
asserts the rewritten credential.

## Spec Notes

- `DATA_MODELS.md` says "four" bundled base images but lists two; the two
  listed presets are seeded.
- Auth endpoints required by `FRONTEND.md` were missing from `BACKEND.md`; they
  are implemented above.
- Interactive shell access is implemented as `GET /instances/{id}/connect`
  (Docker exec, with `POST /instances/{id}/exec/{execID}/resize`) instead of the
  spec's `attach` + `resize`: attach shares PID 1 stdio, so clients would mirror
  one session; exec gives each client an independent shell.
- `config.json` is documented here because `DATA_MODELS.md` omits it.
- `Instance.endpoints` (`InstanceEndpoint`) is an extension to
  `DATA_MODELS.md` needed by the bridge; it stores only dummy tokens.
- The bridge hijacks agent traffic to `127.0.0.1` and relays it from the guest
  to the unprivileged gateway ports, instead of the spec's literal `:443` +
  `iptables`, because the daemon runs unprivileged. See `PROXY.md`.
