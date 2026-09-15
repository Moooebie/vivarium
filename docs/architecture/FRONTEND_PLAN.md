# Vivarium Frontend Plan (Bubble Tea TUI)

Implements `FRONTEND.md`. The TUI is a Go/Bubble Tea client that talks only to
the backend Unix-socket API and shares wire types with it.

## Decisions

- **MVP-first phasing**, then complete screens.
- **Detached backend**: the default `vivarium` run connects to an existing
  daemon, or spawns a detached one (`setsid`, logging to
  `$XDG_STATE_HOME/vivarium/daemon.log`) so it outlives the TUI and can be
  shared by several terminals. `--daemon` runs the backend in the foreground,
  `--stop` stops a running daemon, and an in-process fallback is used if
  spawning fails.
- **Instance editing** is supported by `PUT /api/v1/instances/{id}`: it renames
  and/or rebinds API endpoints. Endpoint changes are applied live (registry +
  `/etc/hosts` + per-exec env) without recreating the container. Mounts and GPUs
  are fixed at creation and shown disabled in the editor.
- **Secret reveal** is supported by `GET /api/v1/api-keys/{id}/secret` (the
  socket is mode `0600`).
- **Vault-only setup.** The first-run flow goes straight to setting a master
  password; the libsecret/System Keyring option is hidden (see
  `STATUS.md` → "libsecret disabled"). The key list reports a `secret_state`
  (`ok`/`locked`/`missing`/`error`) and badges each distinctly, with `u` to
  unlock a locked vault from the Main and API Keys screens.

## Package Layout

```
internal/
  apitypes/        # exported wire types shared by api + client
  version/         # Version string, surfaced via /system/status and --version
  client/          # unix-socket API client
  tui/
    app.go         # root model, screen stack, modal overlay, global keys
    context.go     # deps: client, paths.Env, cwd, version
    theme.go       # lipgloss styles
    keys.go        # keymap
    components/    # list, confirm modal, spinner, log view
    screens/       # main, instances, wizard, instance_edit, recipes,
                   # recipe_editor, apikeys, apikey_editor, baseimages,
                   # auth_setup, unlock
cmd/vivarium/main.go   # mode dispatch: TUI (default) | --daemon | --stop | reset
```

## Backend Additions

| Endpoint | Purpose |
| --- | --- |
| `GET /api/v1/ping` | Readiness probe for the TUI. |
| `GET /api/v1/system/status` | Gains a `version` field. |
| `PUT /api/v1/instances/{id}` | Rename and/or rebind API endpoints (live; no recreate). |
| `GET /api/v1/api-keys/{id}/secret` | Return the stored secret for reveal (unlocked store). |
| `GET /api/v1/instances/{id}/connect` | Upgrade to a raw stream for a new interactive exec shell. |
| `POST /api/v1/instances/{id}/exec/{execID}/resize` | Resize an interactive exec session. |

The endpoints manager is shared between the recipe editor (in-memory) and the
instance editor (applied on exit); the instance editor disables Mounts and GPUs
with an explanatory note, and instance creation warns when GPUs are bound.
`Instance` retains `resources` and `user` from creation.

## Client

`internal/client.Client` wraps `http.Client` with a Unix-socket transport and
exposes typed methods for every route, a structured `APIError`, and
`Connect(id, cols, rows)` returning the hijacked stream and exec ID after the
`101` handshake.

## TUI Architecture

- Root `App` holds `stack []Screen`, an optional `modal`, cached lists, and a
  status/error line. Screens emit navigation messages that the root intercepts.
- Components: `bubbles/list`, `textinput`, `textarea`, `spinner`, confirmation
  modal, log `viewport`.
- Async via `tea.Cmd`; long operations show the working indicator; image builds
  stream to a log viewport.
- Styling via `lipgloss`; modals render over a dimmed screen.
- Mouse: wheel scroll, single-click select, double-click drill.

## Screens

Auth Setup / Unlock, Main, Instances, New Instance Wizard (5 steps), Edit
Instance, Recipes + Recipe Editor, API Keys + form, Base Images — as specified
in `FRONTEND.md`.

## Cross-Cutting

BIOS-style navigation; textfield nav/edit modes with Enter-commit; `.`/`~` path
expansion; uniform `n/d/e/c/s/a/r` shortcuts; discard/quit confirmations;
relative timestamps; working-indicator animation.

## Milestones

- **M0** apitypes, version, backend additions, client + tests.
- **M1** in-process dispatch, auth, Main, Instances, attach.
- **M2** API Keys, Base Images (rebuild stream).
- **M3** Recipes + editor.
- **M4** Wizard + Edit Instance.
- **M5** mouse, confirmations, spinner, `LOGO`, polish.

## Verification

`go build/vet/test/race` and `gofmt`; unit tests for the client and TUI models;
existing backend suites stay green; a live smoke run after M1.

## Completion Plan (M6) — implemented

M1–M5 delivered working screens. The remaining spec gaps and their resolution:

| Gap | Resolution |
| --- | --- |
| Fields are always-edit (§3.3) | Shared nav/edit `field` component across all forms. |
| No on-screen buttons (§3.2) | Shared clickable `buttonBar` (keyboard + mouse). |
| No working indicator (§3.6) | `spinnerModel` wired into async ops via a `busy` state. |
| No discard confirmation (§3.4) | Dirty tracking + `Esc` confirmation. |
| Mouse wheel only (§3.2) | Click-to-select and double-click-to-drill. |
| Wizard has 3 steps (§4.3) | 5 steps with a reusable, ephemeral recipe customizer. |
| Recipe editor omits resources/mounts/GPUs (§4.5) | Sub-editors; endpoints may be defined inline. |
| API-key form lacks Test/Delete (§4.6) | Buttons; test saves first; reveal checkbox. |
| Base images lack status/size (§4.7) | `present` + `size_bytes` added to `GET /base-images`. |
| Edit instance lacks mount removal/warnings (§4.4) | Add/remove mounts, restart warnings, buttons. |

Testability: `tui.API` is an interface implemented by `*client.Client`, so TUI
tests use a fake and assert behaviours such as the ephemeral-customizer
guarantee (zero recipe writes).

Status: all items above are implemented. `internal/tui/field.go` provides the
nav/edit field, `buttons.go` the dual-control button bar, `mouse.go` behaviour
is folded into `menu`/`buttonBar`, `form.go` arbitrates field/button focus, and
the wizard reuses the recipe editor in ephemeral mode via `customizeDoneMsg`.

## Backend Alignment

Following the transparent DNS-hijack + guest-relay change:

- The Recipe Editor (and wizard customizer) no longer injects base-URL env vars
  when a provider key is added; it adds only the provider API-key placeholder.
  Agents use their built-in defaults and the backend injects the real dummy
  token per instance.
- A read-only **Connection Info** screen (hotkey `k` on Instances and Edit
  Instance) lists each endpoint's `mock_url` and dummy `token`, so users can
  configure agents for custom endpoints.

No IPC changes were required; the TUI talks only to the unchanged API.

## UI Refactor

- **Unified vertical list.** Forms are a single vertical list of rows (text
  fields, drill-down actions, and buttons) navigated with `↑/↓`/`Tab`; `Enter`
  edits a field or activates a row. The old "fields use up/down, buttons use
  left/right" split and the horizontal `buttonBar` are gone.
- **Recipe editor.** The main page is a list: **Name** (inline textbox),
  **Base Image**, **Environment Variables**, **API Endpoints**, **Mount
  Points**, **Resources**, **GPUs** — each drill-down opening a dedicated
  editor page instead of bespoke mode keys.
- **Instance wizard.** Two screens: Identity (name + recipe picker) then the
  ephemeral recipe editor, whose final button is **Create & Launch**. The
  redundant Mounts/GPUs steps are removed (the recipe owns them).
- **Edit Instance.** Actions are a vertical list: **API Endpoints** (shared
  manager, applied on exit), **Connection Info**, Start/Halt, Connect, Clone,
  Rename, Inject, Delete. **Mount Points** and **GPUs** are disabled with a note
  because they are fixed at creation.
- **Connect safety.** Connecting to a non-running container is rejected
  (`409`) by the daemon and guarded in the TUI, because Docker's API returns
  `101 UPGRADED` and hangs for stopped containers. Each connect opens an
  independent exec shell (`GET /instances/{id}/connect`), so multiple terminals
  do not mirror one another; resizes target the session's exec ID. The initial
  size is applied after the exec starts (and re-applied by the client on
  connect and `SIGWINCH`), so the session fills the terminal immediately.
- **Secrets visible.** The API-key form shows the secret in plaintext and
  prefills it when editing; the separate reveal toggle was removed. Name
  placeholders read `Input a name…` and names are required.
- **Key testing.** Tests call the provider's models endpoint
  (`{base_url}/models`, `/v1/models` for Anthropic) and report
  valid / authentication-failed / reachable. `POST /api-keys/test` accepts an
  unsaved payload (optional `key_id` for the stored secret) so testing needs no
  name; the inline recipe endpoint editor has provider selection and testing.
