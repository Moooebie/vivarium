# Vivarium: Frontend Architecture & UI/UX Specification

The frontend is an interactive Terminal User Interface (TUI) client. It communicates exclusively with the Go backend via the Unix Domain Socket API. It is decoupled from backend internals, data serialization, proxy mechanics, and container execution.

---

## 1. CLI Arguments & Initialization

The frontend binary accepts command-line flags to manage operational states before launching the interactive terminal interface:

* `vivarium` - Launches the standard TUI session.
* `vivarium --reset-credentials` (or `--reset-vault`) - Prompts the user with a terminal confirmation dialog:
```
WARNING: This will permanently delete all stored API keys, wipe the local vault (or keyring records), and reset the master password.
Are you sure you want to proceed? [y/N]:

```


If confirmed, it calls the backend reset routine (or invokes backend storage wipe routines), clears cached secrets, and exits immediately with code `0`.
* `vivarium --socket ` - Overrides the default backend Unix domain socket path.

---

## 2. Encryption Setup & Master Password Workflows

### 2.1 First-Run Encryption Configuration

When Vivarium starts, it queries `GET /api/v1/auth/status`. If the backend returns `"unconfigured"`:

* The TUI displays a modal screen: **Encryption Setup**.
* The user is presented with a BIOS-style selector:
1. **System Keyring (`libsecret`)** - Best for standard desktop environments with GNOME Keyring, KWallet, or KeePassXC running over D-Bus.
2. **Local Encrypted Vault** - Best for headless servers, minimal distributions, or setups without an active Secret Service daemon.


* If **Local Encrypted Vault** is selected:
* The UI presents two consecutive password textboxes: `Master Password` and `Confirm Master Password` (input characters masked as `••••••••`).
* Validates matching inputs, enforces a non-empty string, and calls `POST /api/v1/auth/setup` with the chosen backend and password.



### 2.2 Vault Unlock Prompt

On subsequent launches where the local vault is used and currently locked:

* The TUI prompts with a single masked input: `Enter Master Password to Unlock Vault`.
* The user inputs the password and hits `Enter`.
* If authentication succeeds (`POST /api/v1/auth/unlock`), the TUI transitions to the Main Page.
* If authentication fails, display `Invalid Master Password. Try again or run 'vivarium --reset-credentials'` and remain on the prompt.

---

## 3. UI/UX Principles & Design System

### 3.1 "BIOS Setup" Navigation Philosophy

The TUI adheres strictly to a hierarchical, single-list drill-down design:

* The user navigates a single list of options at any given time using vertical navigation (`Up` / `Down`).
* Pressing `Enter` on a row selects it or enters a deeper sub-page/sub-menu.
* Pressing `Esc` steps back up to the parent page.
* **Anti-Pattern Ban:** There must be NO multi-axis cursor controls such as "up/down to select row, left/right to change value" within dense forms. Every editable field, multi-option picker, or nested configuration must be accessed by drilling into that specific item with `Enter`.

### 3.2 Unified Input Paradigm

* **Keyboard-First:** Every action is completely accessible via keyboard.
* `Up` / `Down` (or `k` / `j`): Select list item.
* `Enter`: Drill down, toggle item, or execute button action.
* `Esc`: Universal Back action.
* Consistent global shortcuts (see Section 3.5).


* **Mouse Support:**
* All lists must be scrollable via the mouse wheel.
* Single-click to select an item or activate a button.
* Double-click to drill down into a sub-menu or edit a field.


* **Dual-Control Actions:** Critical navigation controls (such as the main menu options and Wizard `Previous` / `Next` controls) must provide both selectable visual buttons on-screen and direct keyboard shortcuts.

### 3.3 Textbox & Editing Conventions

* **Visual Cues:** Inactive text fields appear with a distinct background or border styling.
* **Focus & Edit Modes:**
* Navigating to a text field highlights it.
* Pressing `Enter` engages **Edit Mode**: the styling changes, a cursor appears, and standard cursor navigation (`Left`, `Right`, `Home`, `End`, `Backspace`) is activated.
* Pressing `Enter` again commits the edit and returns to Navigation Mode.


* **Multi-Line Editing (Environment Variables):**
* Text fields supporting multi-line input accept `Alt+Enter` to insert a newline.
* `Up` / `Down` move the cursor across lines during Edit Mode.


* **Client-Side Path Resolution:**
* The backend stores and requires absolute paths.
* On the frontend, host path inputs allow user shortcuts like `.` (current directory) and `~` (user home).
* When the user presses `Enter` to commit a text field, the frontend immediately expands relative paths to canonical absolute paths before displaying them or sending them to the backend.



### 3.4 Back & Exit Rules

* `Esc` always navigates up one level in the menu hierarchy.
* Example: `API Endpoints` -> `Recipe Editor` -> `Recipe List` -> `Main Page` -> `Exit`.


* **Discard Confirmation:** If `Esc` is pressed while inside an active editing screen or the Instance Creation Wizard and unsaved changes exist, display a confirmation prompt:
```
Discard all unsaved changes and go back? [y/N]

```


* `q` or `Ctrl-C`:
* On the Main Page: Immediately exits the application.
* On any sub-page: Displays a confirmation prompt:
```
Terminate Vivarium session? [y/N]

```





### 3.5 Global Shortcut Consistency

Action keys must remain uniform across all views and lists:

* `n`: New item (New Instance, New Recipe, New Key, New Image).
* `d`: Delete / Remove highlighted item (always prompts confirmation).
* `e` or `Enter`: Edit / Drill into highlighted item.
* `c`: Clone highlighted item.
* `s`: Start / Halt toggle (Instances).
* `a`: Attach interactive shell (Instances).
* `r`: Refresh list or Rebuild standard presets.


### 3.6 "Working" indicator

When the user triggers an action that takes time to complete, e.g. building a Docker image, starting/halting an instance, display an ASCII animation to indicate a job is in progress.

---

## 4. Screen Specifications

### 4.1 Main Page

* **Header:** Reads and renders ASCII art directly from the `./LOGO` file located in the working directory. If `./LOGO` is missing, falls back to plain bold title text.
* **Metadata:** Displays current Vivarium version and backend status (connected socket path, active container count, encryption mode: `libsecret` or `vault (unlocked)`).
* **Menu Options:** Rendered as a selectable vertical list of buttons with hotkeys:
* `[1]` **Instances** - Manage running and halted sandboxes.
* `[2]` **Recipes** - View and configure blueprints.
* `[3]` **API Keys** - Manage upstream credentials and mock routing.
* `[4]` **Base Images** - Manage and build guest images.
* `[Q]` **Quit** - Exit Vivarium.



---

### 4.2 Instances Screen

Lists all managed containers.

#### Ordering Rules:

1. **Pinned Top Section:** Instances that have the host's current working directory (`$PWD`) mounted anywhere in their file system.
2. **Status Section:** Instances with status `running` appear before `halted`.
3. **Alphabetical:** Natural alphabetical sort by instance `name`.

#### Information Displayed:

* Instance Name
* Status Indicator (distinct visual indicators for `RUNNING`, `HALTED`, `BUILDING`, `ERROR`)
* Base Image name/tag
* Last run timestamp (relative format, e.g., `5m ago`)
* Container disk space usage (retrieved from backend)
* PWD mounted indicator

#### Actions:

* `n`: Launch New Instance Wizard.
* `e` / `Enter`: Open Edit Instance page.
* `s`: Toggle Start / Halt.
* `a`: Attach interactive terminal shell.
* `d`: Delete instance (with confirmation modal).

---

### 4.3 New Instance Creation Wizard

A linear, multi-step flow that provisions an instance using a loaded recipe:

1. **Step 1: Identity & Recipe Selection**
* Textbox: `Instance Name` (pre-filled with generated default).
* Selector: List of saved recipes + `(none)` for a blank configuration.
* Selectable buttons: `[ Next > ]` (or press `Enter` on Next).


2. **Step 2: Ephemeral Recipe Customizer**
* Loads the selected recipe values into an in-memory temporary recipe object.
* The user can drill into fields to make last-minute edits:
* Base Image selector
* API Endpoints manager
* Environment Variables editor (multi-line)
* Resources to inject
* Directory mounts
* GPU allocations


* **Ephemeral Guarantee:** Edits made here are strictly applied to the instance being created. They are **never** saved back to `recipes.json`.
* Selectable buttons: `[ < Back ]`, `[ Next > ]`.


3. **Step 3: Mounts Confirmation & Validation**
* Displays all mounts configured for this instance.
* Allows adding additional directory mappings:
* Host path input auto-expands `.` and `~`.
* Guest path input provides a dim placeholder defaulting to `/mnt/` if left empty.
* Mode toggle: `rw` or `ro`.


* Selectable buttons: `[ < Back ]`, `[ Next > ]`.


4. **Step 4: GPU Device Allocation**
* Lists detected host AMD GPUs retrieved via `GET /api/v1/system/gpus`.
* Displays device model name, PCI ID, and rendering node.
* Toggle selection on or off per device.
* Selectable buttons: `[ < Back ]`, `[ Create & Launch ]`.


5. **Step 5: Shell Attachment**
* Upon creation, the backend provisions and starts the container.
* The frontend immediately suspends TUI raw mode and transitions terminal I/O to the interactive PTY stream (`/api/v1/instances/{id}/attach`).
* When the shell session terminates or the user detaches, the frontend restores the TUI state cleanly to the Instances screen.



---

### 4.4 Edit Instance Screen

Allows inspecting and mutating an existing instance.

* **Status Bar:** Displays container ID, IP address on `vivarium-net`, and runtime state.
* **Top Actions:** Buttons for `[ Start / Halt ]`, `[ Attach Shell ]`, `[ Clone ]`, `[ Delete ]`.
* **Configuration Sections (Drill-down via Enter):**
* **Mount Points:** View active mounts. Add new mounts (warns that a restart is required if running).
* **Live File Injection:** Select a host file (with path auto-expansion) and target guest path; calls `POST /api/v1/instances/{id}/inject` to copy files immediately without container restarts.
* **GPU Allocation:** Toggle AMD GPU assignments (requires container restart).
* **Metadata:** Rename instance.



---

### 4.5 Recipes Screen & Recipe Editor

#### Listing:

* Lists all saved recipes sorted alphabetically.
* Actions: `n` (New Recipe), `Enter` (Edit Recipe), `c` (Clone), `d` (Delete).

#### Recipe Editor:

* **Header:** Textbox for recipe name, buttons for `[ Save ]`, `[ Delete ]`.
* **Drill-Down Configuration List:**
* **Base Image:** Opens list of registered base images. Selecting one sets `base_image_id`.
* **API Endpoints:** Opens list of mapped API Key objects.
* Adding an endpoint allows picking an existing saved API Key or defining one inline.
* **Validation:** Frontend prevents adding two endpoints with conflicting `mock_url` values.


* **Environment Variables:** Opens key-value editor with multi-line support (`Alt+Enter`).
* **Resources:** Opens file injection list (Host Source, Guest Target, File Mode).
* **Default Mounts:** Opens default directory mount list.
* **GPUs:** Opens list of host GPUs to mark for automatic assignment.



---

### 4.6 API Keys Screen

Manages credentials stored across `api_keys.json` and the active encryption store (`libsecret` or Local Vault).

#### Listing:

* Lists saved API keys showing Name, Provider Type, Mock URL, and Rate Limit.
* Actions: `n` (New Key), `Enter` (Edit Key), `d` (Delete Key), `t` (Test Connection).

#### Key Form / Editor:

* `Name`: String identifier.
* `Provider Type`: Picker (`openai`, `anthropic`, `deepseek`, `openrouter`, `custom`).
* `Base URL`: True upstream endpoint (e.g., `https://api.openai.com/v1`).
* `Mock URL`: The URL the container will query (e.g., `https://api.openai.com/v1`).
* `Secret Key`: Masked input field (`••••••••`).
* Includes a checkbox / toggle: `[ ] Reveal Secret`.


* `Rate Limit (RPM)`: Numeric input (0 or negative to disable).
* Actions:
* `[ Save Key ]`: Sends metadata and secret to backend. Stored in keyring or vault.
* `[ Test Connection ]`: Requests backend execute a test request to upstream provider. Shows test status modal (Success / HTTP error code).
* `[ Delete Key ]`: Calls backend deletion (removes from metadata and keyring/vault).



---

### 4.7 Base Images Screen

Administers container base runtimes.

#### Listing:

* Lists images separated into two sections: **Standard Presets** and **Custom Images**.
* Displays: Image Tag, Type (`Standard`, `DockerHub`, `Dockerfile`), Status, Size.

#### Actions:

* `n`: Register Custom Image:
* Select Source: `Docker Hub` or `Local Dockerfile`.
* Input repository string or local file path (supports path auto-expansion).


* `r`: Trigger rebuild of standard preset images (displays progress bar).
* `d`: Remove custom image registration (standard presets cannot be removed).
