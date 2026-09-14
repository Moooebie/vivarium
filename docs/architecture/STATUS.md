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
| Auth lifecycle | `internal/auth` | `unconfigured` / `locked` / `unlocked`, setup, unlock, reset. |
| Docker | `internal/docker` | Direct Engine API client, `vivarium-net`, GPU enum, mounts, runtime `/etc/hosts` sync, live endpoint resync, resource/file injection, detached exec, interactive exec sessions (connect, per-session env), CA trust injection, guest-relay lifecycle, standard image builds. |
| Host bridge | `internal/proxy` | Rootless TLS MITM, CA + leaf minting, endpoint registry, dummy-token validation, credential injection, rate limiting, SSE streaming. |
| Guest relay | `cmd/vivarium-guestbridge` | Static TCP forwarder hijacking agent traffic (`127.0.0.1:443`/`:80`) to the unprivileged host bridge; embedded via `internal/guestbridge`. |
| IPC API | `internal/api` | Full `BACKEND.md` §3 surface plus auth endpoints, `ping`, instance `PUT`, secret reveal, and per-session exec connect/resize. |
| Wire types | `internal/apitypes` | JSON DTOs shared by the API and the client. |
| API client | `internal/client` | Typed Unix-socket client with exec connect upgrade. |
| TUI | `internal/tui` | Bubble Tea frontend: auth, Main, Instances, Wizard, Edit, Recipes, API Keys, Base Images, Connection Info, connect shell. Unified vertical row forms; recipe editor drill-down pages. |
| Daemon | `internal/daemon` | Unix socket listener, `flock` single-instance lock, PID file, bridge lifecycle, graceful shutdown. |
| CLI | `cmd/vivarium` | Default TUI with detached auto-spawned backend; `--daemon`, `--stop`, `--version`, `--socket`, reset flags, proxy flags. |

See `PROXY.md` for the bridge design and `FRONTEND_PLAN.md` for the TUI.

## Deferred / Follow-ups

- Optional: mock D-Bus Secret Service test harness (`dbusmock`).

### libsecret disabled (UI)

The TUI now offers only the encrypted vault. The libsecret backend remains in
`internal/keyring` (and is still reachable through `POST /auth/setup`), but it is
hidden in the UI pending fixes. Observed problems and the intended fixes:

- **Ephemeral collection risk.** Items created before the persistent-collection
  fix could live in the `session` collection, which is wiped when the keyring
  session ends, leaving JSON metadata pointing at a secret that no longer
  exists (the reported "keys vaporate"). Ensure storage always targets a
  persistent collection and verify the created item's collection after write.
- **Alias-dependent exclusion.** `choosePersistentCollection` excludes the
  session collection only via the `session` alias; if that alias is unreadable
  the ephemeral collection can be selected. Exclude
  `/org/freedesktop/secrets/collection/session` by absolute path instead.
- **Connection lifecycle.** `LibSecret` uses the shared `godbus` session
  connection and never reconnects; if gnome-keyring restarts or the session is
  invalidated, lookups fail until the process restarts. Own a private,
  reconnectable connection and retry once on transient errors.
- **Bus identity.** Different run contexts (SSH vs desktop) can have different
  `DBUS_SESSION_BUS_ADDRESS`, so the same user sees different keyrings. Log the
  bus address and resolved collection at startup.

Until these are addressed, use the vault. See also the `keyring` integration
tests, which still exercise libsecret directly.

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
  unprivileged gateway ports (`:8443`/`:8080`). Each container gets a small
  root-started TCP relay (`127.0.0.1:443`/`:80` → gateway) so agents use their
  **default** base URLs, and mock hosts are written into a managed `/etc/hosts`
  block at runtime (`SyncHosts`). No host root and no `iptables`. See `PROXY.md`.
- **Live endpoints (no recreate).** Changing an instance's API endpoints only
  updates the host proxy registry, the guest `/etc/hosts`, and the provider env
  of new exec sessions — the container is never recreated. Provider dummy tokens
  are injected per exec (`Connect`) instead of being stored in the container
  environment. `/etc/hosts` is re-synced after create and after every start
  (Docker regenerates it on start).
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
- **Mounts and GPUs are fixed at creation.** Docker cannot add binds or devices
  to a running container, and cannot mutate `Config.Env`/`HostConfig`; recreating
  would discard the writable layer. The instance editor disables these actions
  (grayed out) and instance creation warns when GPUs are bound; use a new
  instance to change them. Endpoints are exempt because their effects
  (proxy route, `/etc/hosts`, exec env) are reproducible at runtime.
  - **TODO (future):** a privileged host-side helper could change device (and
    mount) binding without recreating the container (for example a `mount --bind`
    inside the container's mount namespace and a device-cgroup allowance).
    Discouraged and not implemented.
- **Connect = exec, not attach.** Interactive shells use Docker's exec API
  (`/containers/{id}/exec` + `/exec/{id}/start` upgrade), so every client gets
  an independent shell. Container attach (PID 1 stdio) would mirror sessions.
  The exec ID is returned in the `X-Vivarium-Exec` header for per-session
  resizes. The shell is `bash -l` when present, else `sh`.
- **Detached backend.** The TUI connects to an existing daemon or spawns one
  with `setsid` (logging to `$XDG_STATE_HOME/vivarium/daemon.log`) so it
  outlives the TUI and can be shared by multiple terminals. The daemon writes a
  PID file for `--stop`. An in-process fallback remains if spawning fails.
- **No vault auto-lock.** The vault stays unlocked for the daemon's lifetime;
  there is no idle re-lock. This avoids the vault appearing to "lose" secrets
  mid-session (a locked store previously surfaced as `has_secret=false`).
- **Secret state, not a boolean.** `GET /api-keys` reports `secret_state`
  (`ok` / `locked` / `missing` / `error`) so a locked store is never mistaken
  for a missing secret. The TUI badges them distinctly (`MISSING`,
  `LOCKED`, `SECRET ERROR`) and offers an unlock action.
- **Startup secret audit.** The daemon logs any API key whose secret is absent
  (`keyring.ErrNotFound`) at startup, so a lost credential is visible
  immediately instead of surfacing later as proxy failures.

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
PUT    /instances/{id}                 # rename and/or rebind endpoints (live)
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
