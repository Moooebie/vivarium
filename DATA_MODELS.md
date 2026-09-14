# Vivarium: Data Models & Storage Schema

This document outlines the data structures, schema validations, and storage mechanisms for Vivarium. 

## 1. Storage Overview

Vivarium splits persistence into two tiers:
1. **Metadata & Structured Configurations:** Stored as JSON files under `$XDG_DATA_HOME/vivarium/` (defaulting to `~/.local/share/vivarium/`) with strict `0600` file permissions.
2. **Sensitive Secret Material:** Upstream API keys are never written to disk in plaintext or JSON. They are offloaded to the host operating system's credential manager via `libsecret` (FreeDesktop Secret Service API).

### Hierarchy & Entity Lifecycle
- **Base Image** and **API Key** definitions serve as atomic building blocks.
- **Recipe** models combine a Base Image reference, embedded API Key specifications, environment variables, static resource files, default mount points, and GPU requirements into a reusable container blueprint.
- **Instance** models represent live or persisted Docker containers provisioned from a Recipe (or configured on the fly).

---

## 2. Model Schemas

### 2.1 Base Image (`base_images.json`)

Defines the base container runtime.

```json
{
  "id": "uuid4",
  "name": "Ubuntu 24.04 + OpenCode (Unconstrained)",
  "source_type": "standard", // "standard" | "dockerhub" | "dockerfile"
  "source_path_or_repo": "vivarium/ubuntu-24.04-opencode:latest",
  "default_user": "root",
  "is_internal": true
}

```

#### Fields:

* `id` (string, UUIDv4, required): Unique identifier.
* `name` (string, required): Human-readable display label.
* `source_type` (string, required): Source origin (`standard`, `dockerhub`, or `dockerfile`).
* `source_path_or_repo` (string, required): Docker Hub image repository string or absolute host path to a local Dockerfile.
* `default_user` (string, required): Initial container user (typically `root`).
* `is_internal` (boolean, required): `true` if this is one of the four pre-bundled Vivarium base images; `false` if user-registered.

#### Standard Bundled Presets:

1. `Ubuntu 24.04 LTS + OpenCode`: Ubuntu 24.04 minimal base with OpenCode runtime.
2. `Ubuntu 24.04 LTS + Pi`: Ubuntu 24.04 minimal base with Pi agent runtime.

---

### 2.2 API Keys (`api_keys.json` & `libsecret`)

#### Metadata File (`api_keys.json`):

```json
{
  "id": "uuid4",
  "name": "Work OpenAI Account",
  "provider_type": "openai", // "openai" | "anthropic" | "deepseek" | "openrouter" | "custom"
  "base_url": "[https://api.openai.com/v1](https://api.openai.com/v1)",
  "mock_url": "[https://api.openai.com/v1](https://api.openai.com/v1)",
  "rate_limit_rpm": 60,
  "created_at": 1773440000
}

```

#### Fields:

* `id` (string, UUIDv4, required): Unique identifier.
* `name` (string, required): User-facing alias for the key.
* `provider_type` (string, required): Credential archetype (`openai`, `anthropic`, `deepseek`, `openrouter`, `custom`).
* `base_url` (string, required): The true external provider endpoint to which the host bridge will proxy requests.
* `mock_url` (string, required): The internal URL the agent container will query. The host bridge intercepts this URL and rewrites it to `base_url`.
* `rate_limit_rpm` (integer, optional): Maximum requests per minute allowed through the bridge proxy for this key. Value `<= 0` disables rate limiting.
* `created_at` (int64, required): Unix timestamp of creation.

#### Keyring Secret Storage (`libsecret`):

The actual token or secret string is stored in the system keyring via `libsecret`:

* **Schema Name:** `org.vivarium.ApiKey`
* **Lookup Attribute:** `vivarium_key_id = `
* **Secret Value:** Raw API key token (e.g., `sk-proj-...`).

---

### 2.3 Recipe (`recipes.json`)

The complete blueprint for constructing an agent sandbox.

```json
{
  "id": "uuid4",
  "name": "Standard Python Agent",
  "base_image_id": "uuid4",
  "api_endpoints": [
    {
      "id": "uuid4",
      "name": "Work OpenAI Account",
      "provider_type": "openai",
      "base_url": "[https://api.openai.com/v1](https://api.openai.com/v1)",
      "mock_url": "[https://api.openai.com/v1](https://api.openai.com/v1)",
      "rate_limit_rpm": 60,
      "created_at": 1773440000
    }
  ],
  "env_vars": {
    "DEBIAN_FRONTEND": "noninteractive",
    "PYTHONUNBUFFERED": "1"
  },
  "resources": [
    {
      "host_source_path": "/home/user/.bashrc",
      "guest_target_path": "/root/.bashrc",
      "file_mode": "0644"
    }
  ],
  "default_mounts": [
    {
      "host_path": "/home/user/projects/repo",
      "guest_path": "/mnt/repo",
      "mode": "rw"
    }
  ],
  "gpus": [
    {
      "device_id": "0000:03:00.0",
      "name": "AMD Radeon Pro / AI Series",
      "render_node": "/dev/dri/renderD128"
    }
  ]
}

```

#### Fields:

* `id` (string, UUIDv4, required): Unique identifier.
* `name` (string, required): Recipe name.
* `base_image_id` (string, UUIDv4, required): Reference to an entry in `base_images.json`.
* `api_endpoints` (array of API Key objects, required): Full API Key objects embedded directly. **Constraint:** No two entries in the array may have the same `mock_url`.
* `env_vars` (map of string to string, required): Environment variables set during container creation.
* `resources` (array of objects, required): Host files copied into the guest file system on container creation:
* `host_source_path` (string, required): Canonical absolute path on host.
* `guest_target_path` (string, required): Destination path inside guest.
* `file_mode` (string, required): Octal permission bits (e.g., `0644`, `0755`).


* `default_mounts` (array of objects, required): Directory bind mounts:
* `host_path` (string, required): Canonical absolute path on host.
* `guest_path` (string, required): Canonical absolute path inside guest.
* `mode` (string, required): Mount permission mode (`ro` for read-only, `rw` for read-write).


* `gpus` (array of GPU objects, required): GPUs allocated to instances using this recipe (see section 2.5).

---

### 2.4 Instance (`instances.json`)

Represents an active, halted, or persisted container managed by Vivarium.

```json
{
  "id": "uuid4",
  "container_id": "docker_hash_or_empty",
  "name": "dev-agent-sandbox-1",
  "recipe_id": "uuid4_or_null",
  "status": "running",
  "created_at": 1773440100,
  "last_run_at": 1773445000,
  "base_image_tag": "vivarium/ubuntu-24.04-opencode:latest",
  "mounts": [
    {
      "host_path": "/home/user/projects/repo",
      "guest_path": "/mnt/repo",
      "mode": "rw"
    }
  ],
  "gpus": [
    {
      "device_id": "0000:03:00.0",
      "name": "AMD Radeon Pro / AI Series",
      "render_node": "/dev/dri/renderD128"
    }
  ],
  "ip_address": "172.28.0.2",
  "env_vars": {
    "DEBIAN_FRONTEND": "noninteractive",
    "PYTHONUNBUFFERED": "1",
    "OPENAI_API_BASE": "[https://api.openai.com/v1](https://api.openai.com/v1)",
    "OPENAI_API_KEY": "viv-tok-a9f81b..."
  }
}

```

#### Fields:

* `id` (string, UUIDv4, required): Internal Vivarium identifier.
* `container_id` (string, required): 64-character Docker container hash (or empty string if not yet materialized).
* `name` (string, required): Container name.
* `recipe_id` (string, UUIDv4 or null, required): Originating recipe ID, or `null` if created without a saved template.
* `status` (string, required): Execution status (`running`, `halted`, `building`, `error`).
* `created_at` (int64, required): Creation timestamp.
* `last_run_at` (int64, required): Timestamp when container last entered `running` status.
* `base_image_tag` (string, required): Docker image tag used to run the container.
* `mounts` (array of Mount objects, required): Active bind mounts.
* `gpus` (array of GPU objects, required): Active GPU device allocations.
* `ip_address` (string, required): Assigned IP on the `vivarium-net` bridge (e.g., `172.28.0.2`).
* `env_vars` (map of string to string, required): Full set of container environment variables, including generated dummy tokens mapping to intercepted endpoints.

---

### 2.5 Supporting Structure: GPU Object

```json
{
  "device_id": "0000:03:00.0",
  "name": "AMD Radeon Pro / AI Series",
  "render_node": "/dev/dri/renderD128"
}

```

* `device_id` (string): PCI bus address identifier.
* `name` (string): Human-readable device model resolved from host PCI database.
* `render_node` (string): Primary direct rendering infrastructure device path.


