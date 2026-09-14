# Vivarium: Backend Architecture & Implementation Specification

The Vivarium backend is a standalone Go daemon/service that owns data persistence, secret management, host-bridge networking, dynamic TLS interception, Docker container lifecycle, and hardware passthrough. It exposes a Unix Domain Socket API consumed by the frontend.

## 1. System Requirements & Testing Standards

- **Language:** Go 1.22+
- **Host Platform:** Linux (x86_64) with systemd/D-Bus, Docker Engine, and AMD ROCm/DRI drivers.
- **Testing Standard:** Thorough automated test coverage across all subsystems:
  - Unit tests for model validations and JSON serialization.
  - Dynamic TLS MITM proxy tests using mock HTTP upstreams.
  - Keyring tests using mock Secret Service D-Bus interfaces.
  - Docker integration tests verifying mock network creation, volume mounts, and `--add-host` configurations.

---

## 2. Subsystem Architectures

### 2.1 Storage & Secret Store (`internal/store`, `internal/keyring`)

To accommodate both desktop environments with active D-Bus Secret Services and headless systems (minimal servers, SSH sessions, container hosts without desktop daemons), the backend provides an interchangeable credential storage interface (`SecretStore`) with two supported backends:

```
                      ┌─────────────────────────┐
                      │  SecretStore Interface  │
                      └────────────┬────────────┘
                                   │
              ┌────────────────────┴────────────────────┐
              ▼                                         ▼
   ┌──────────────────────┐                  ┌──────────────────────┐
   │  libsecret Backend   │                  │  Local Vault Backend │
   │ (FreeDesktop D-Bus)  │                  │  (Argon2id + GCM)    │
   └──────────────────────┘                  └──────────────────────┘

```

The active provider is configured in `$XDG_DATA_HOME/vivarium/config.json` (`"secret_storage_type": "libsecret" | "vault"`).

#### 1. `libsecret` Backend (Default for Desktop)

* **Integration:** D-Bus bindings to `org.freedesktop.secrets` with schema `org.vivarium.ApiKey`.
* **Mapping:** Maps `vivarium_key_id` to the encrypted secret.
* **Operations:**
* `StoreSecret(keyID string, secret string) error`
* `GetSecret(keyID string) (string, error)`
* `DeleteSecret(keyID string) error`



#### 2. Local Encrypted Vault Backend (Headless / Standalone)

* **Storage Location:** `$XDG_DATA_HOME/vivarium/vault.enc` (file mode `0600`).
* **Key Derivation (KDF):** Argon2id with parameters:
* Memory: 64 MB
* Iterations: 3
* Parallelism: 4
* Salt: 16 cryptographically secure random bytes stored in the file header.


* **Authenticated Encryption:** AES-256-GCM (or ChaCha20-Poly1305) using a 96-bit random nonce per write. The ciphertext contains a serialized JSON map of `map[string]string` (`keyID -> secret`).
* **Master Password Lifecycle:**
* On startup with vault enabled, the backend checks whether `vault.enc` exists. If it exists, the vault remains in a `Locked` state until unlocked via API or master password prompt.
* Once unlocked, derived keys and secrets are cached in memory protected by `mlock` (to prevent swapping to disk).
* When the backend terminates, memory buffers are explicitly zeroed out (`memguard` or explicit byte zeroing).


* **Reset Mechanism (`--reset-credentials` / `--reset-vault`):**
* Invoked via CLI: `vivarium --reset-credentials`
* Securely unlinks and wipes `$XDG_DATA_HOME/vivarium/vault.enc`.
* Resets all `api_keys.json` records to clear out saved secret IDs or strips them.
* If `libsecret` is in use, queries all items matching schema `org.vivarium.ApiKey` and executes `secret_password_clear` for each.
* Wipes cached tokens, resetting the encryption setup state for the next run.


---

### 2.2 Host Bridge & Isolation Proxy (`internal/proxy`)

```
[ Guest Container (IP: 172.28.0.2) ]
       │
       │ HTTP/HTTPS to [https://api.openai.com/v1](https://api.openai.com/v1) (Auth: Bearer viv-tok-...)
       ▼
[ Host Bridge Proxy ]
       │
       ├─► 1. Match source IP 172.28.0.2 to active Instance
       ├─► 2. Intercept TLS using dynamic leaf certificate signed by Vivarium CA
       ├─► 3. Validate dummy token from Authorization header
       ├─► 4. Match request against mock_url; route to target base_url
       ├─► 5. Inject real API key from keyring
       ├─► 6. Enforce rate_limit_rpm (if > 0)
       ▼
[ Upstream Provider ([https://api.openai.com/v1](https://api.openai.com/v1)) ]

```

#### Networking & DNS Hijacking:

1. **Network Provisioning:** Ensures a user-defined Docker bridge network named `vivarium-net` exists:
* Subnet: `172.28.0.0/16`
* Gateway: `172.28.0.1`


2. **DNS Override:** For every `mock_url` attached to a container, the backend parses the hostname and passes `--add-host :172.28.0.1` to the Docker daemon.
3. **Transparent Redirection:** Container requests to port `443` on `172.28.0.1` hit the proxy listener directly, or are routed via host `iptables` rules:
```bash
iptables -t nat -A PREROUTING -i vivarium-net -p tcp --dport 443 -j REDIRECT --to-port 8443

```



#### Dynamic TLS Interception:

* **CA Generation:** Generates an RSA 4096-bit local root certificate and private key at `$XDG_DATA_HOME/vivarium/certs/vivarium-ca.{crt,key}` if absent.
* **Dynamic Leaf Minting:** On TLS `ClientHello`, reads the Server Name Indication (SNI), mints an in-memory leaf certificate matching the requested hostname signed by the Vivarium CA, and caches the certificate.
* **Guest CA Trust:** Injects `vivarium-ca.crt` into the container at `/usr/local/share/ca-certificates/vivarium-ca.crt`, updates standard trust stores (`update-ca-certificates`), and exports `SSL_CERT_FILE`, `REQUESTS_CA_BUNDLE`, and `NODE_EXTRA_CA_CERTS`.

#### Request Processing & Proxy Pipeline:

1. Identify calling container by checking client socket remote IP against active instance network leases.
2. Match incoming request URL against the instance's active `api_endpoints` list (`mock_url`).
3. Validate and strip the container's dummy bearer token (`viv-tok-...`).
4. Retrieve the corresponding real API key from the keyring.
5. Re-write the request host, path, and `Authorization` header to match `base_url`.
6. Stream request and response bidirectionally, supporting Server-Sent Events (SSE) for streaming completions without buffering.

---

### 2.3 Docker Engine Controller (`internal/docker`)

* **AMD GPU Enumeration & Passthrough:**
* Scans `/sys/class/drm/renderD*/device/vendor` for vendor ID `0x1002` (AMD).
* Correlates with `/sys/bus/pci/devices/` and `lspci` to obtain human-readable model names and PCI IDs (`device_id`).
* Passes devices into container via:
```bash
--device=/dev/kfd --device=/dev/dri --group-add video --group-add render --security-opt seccomp=unconfined --ipc=host

```




* **Static Resource Injection:**
* Injects files specified in `resources` by constructing in-memory tarballs and calling the Docker Copy Archive API (`PUT /containers/{id}/archive`).
* Sets exact permissions specified by `file_mode`.


* **Mount Management:**
* Rejects relative paths. All mount points passed to the backend must be absolute canonical paths.
* Mounts are applied as bind mounts (`type=bind,source=,target=,readonly=`).


* **Standard Image Builder:**
* Builds pre-packaged standard images if not present locally using embedded Dockerfiles.
* Streams build logs to API consumers.



---

## 3. IPC API Specification

The backend serves a RESTful JSON API over a Unix Domain Socket located at:
`/run/user//vivarium/vivarium.sock` (fallback: `/tmp/vivarium-.sock`)

### 3.1 Base Images

* `GET /api/v1/base-images` - List all registered and standard base images.
* `POST /api/v1/base-images` - Register a custom image (Docker Hub or Dockerfile path).
* `DELETE /api/v1/base-images/{id}` - Deregister custom base image.
* `POST /api/v1/base-images/rebuild` - Trigger rebuild of standard images.

### 3.2 API Keys

* `GET /api/v1/api-keys` - List API key metadata (secrets omitted).
* `POST /api/v1/api-keys` - Save API key metadata and store secret in `libsecret`.
* *Payload:* Metadata fields + `"secret": "sk-proj-..."`.


* `PUT /api/v1/api-keys/{id}` - Update metadata or update secret.
* `DELETE /api/v1/api-keys/{id}` - Remove metadata and clear secret from `libsecret`.
* `POST /api/v1/api-keys/{id}/test` - Perform host-side connection test to `base_url`.

### 3.3 Recipes

* `GET /api/v1/recipes` - List all saved recipes.
* `POST /api/v1/recipes` - Create a new recipe.
* `GET /api/v1/recipes/{id}` - Retrieve recipe details.
* `PUT /api/v1/recipes/{id}` - Update an existing recipe.
* `DELETE /api/v1/recipes/{id}` - Delete a recipe.

### 3.4 Instances

* `GET /api/v1/instances` - List instances with statuses, disk usage, and mount info.
* `POST /api/v1/instances` - Provision and start a new instance.
* *Payload:* Accepts an `Instance` creation object, or an ephemeral in-memory `Recipe` configuration with instance name, mounts, and GPU choices.


* `GET /api/v1/instances/{id}` - Get detailed instance state.
* `POST /api/v1/instances/{id}/start` - Start halted instance.
* `POST /api/v1/instances/{id}/halt` - Halt running instance.
* `DELETE /api/v1/instances/{id}` - Stop and remove container and associated resources.
* `POST /api/v1/instances/{id}/inject` - Live-inject a file into a running container.
* `GET /api/v1/instances/{id}/attach` - Upgrade connection to WebSocket / raw PTY stream for interactive shell access.

### 3.5 System & Hardware

* `GET /api/v1/system/gpus` - Returns list of detected AMD GPUs on host.
* `GET /api/v1/system/status` - Returns Docker status, Keyring accessibility, and CA cert details.

