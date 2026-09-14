# Vivarium Host Bridge / Reverse Proxy

Implements `BACKEND.md` §2.2: a rootless, TLS-intercepting reverse proxy that
lets sandboxed agents call upstream providers with dummy tokens while Vivarium
injects the real credentials and enforces rate limits.

## DNS Hijack + Guest Relay (transparent port)

The daemon runs unprivileged, so the bridge cannot bind the gateway's port `443`
and installs no `iptables` rules. Instead, agent traffic is hijacked **inside the
guest** and relayed to the unprivileged host listener:

```
agent requests  : https://api.openai.com/v1        (agent's own default URL)
/etc/hosts      : api.openai.com -> 127.0.0.1      (runtime sync, see below)
guest relay     : 127.0.0.1:443  ->  172.28.0.1:8443   (raw TCP)
                  127.0.0.1:80   ->  172.28.0.1:8080
host proxy      : 172.28.0.1:8443 (TLS), 172.28.0.1:8080 (plain)
```

The `/etc/hosts` entries are **not** set with `--add-host`; they are written at
runtime by `Controller.SyncHosts` into a `# vivarium`-marked block, so endpoints
can change without recreating the container. Docker regenerates `/etc/hosts` on
start, so the block is re-applied after create and after every start. Provider
dummy tokens are likewise injected per exec session (`Connect`), not baked into
the container environment.

- The relay is a tiny static Go binary (`cmd/vivarium-guestbridge`), injected at
  `/usr/local/bin/vivarium-guestbridge` and started detached **as root** after
  the container starts; the agent keeps running as the container's configured
  user.
- It is a dumb TCP forwarder: TLS is end-to-end between the agent and the host
  proxy, so SNI/Host are preserved and the proxy performs all interception.
- Because the relay dials from the container's network namespace, the proxy
  still sees the container IP and its source-IP → instance mapping is unchanged.
- No base-URL environment variables or per-agent config are injected. Agents use
  their built-in provider defaults; **custom endpoints are the user's
  responsibility to configure** (Vivarium only makes `mock_url` reachable and
  proxies it to `base_url`).

## TLS Interception

- **CA:** `$XDG_DATA_HOME/vivarium/certs/vivarium-ca.{crt,key}`. RSA-4096,
  10-year validity, `KeyUsageCertSign|CRLSign`, key mode `0600`, cert `0644`.
  Generated on first start and reused thereafter.
- **Leaf minting:** on each TLS `ClientHello`, a P-256 leaf certificate is
  minted for the SNI hostname (`DNSNames`/`IPAddresses`), 30-day validity,
  signed by the CA, and cached until near expiry.
- **Upstream:** the guest leg is intercepted; the upstream leg is a normal
  HTTPS request whose provider certificate is verified against system roots.

## Guest Trust Injection

At instance creation the API layer sets:

```
SSL_CERT_FILE       = /etc/ssl/certs/vivarium-ca.pem
REQUESTS_CA_BUNDLE  = /etc/ssl/certs/vivarium-ca.pem
NODE_EXTRA_CA_CERTS = /etc/ssl/certs/vivarium-ca.pem
```

The CA is also written to
`/usr/local/share/ca-certificates/vivarium-ca.crt` via the Docker Copy Archive
API, and `update-ca-certificates` is executed (best-effort) after start so
tools that rely on the system trust store also work.

## Request Pipeline

1. Identify the calling instance by socket remote IP (`Registry.Match`).
2. Match the request host/path against the instance's endpoints
   (longest path-prefix wins).
3. Validate the dummy token (`viv-tok-…`) from `Authorization: Bearer` or
   `x-api-key` in constant time.
4. Enforce `rate_limit_rpm` with a per-key token bucket.
5. Fetch the real secret from the active `SecretStore`.
6. Rewrite the target to `base_url`, set `Host`, and replace the dummy token
   with the real credential (`anthropic-version` is added for Anthropic).
7. Stream request and response bidirectionally with `FlushInterval = -1` so
   Server-Sent Events are not buffered.

## Credentials in the Guest

Only the dummy provider API-key variable is injected for standard providers
(`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `DEEPSEEK_API_KEY`, `OPENROUTER_API_KEY`).
Base URLs are never injected. The real secret stays in the keyring/vault and is
only ever added by the proxy on the upstream leg.

## Endpoint Persistence

`Instance` carries an `endpoints` array of `InstanceEndpoint`
(`key_id`, `provider_type`, `base_url`, `mock_url`, `token`, `rate_limit_rpm`).
Only the dummy token is persisted; real secrets stay in the keyring/vault.
The registry is rebuilt from these records at startup (`Reconcile`), resolving
live container IPs from Docker.

## Lifecycle

- The relay is started immediately after the container starts and a `-check`
  readiness probe must pass before the instance is reported created; any failure
  aborts creation and removes the container.
- Detached exec processes do not survive a stop/start, so the relay is
  re-started when a halted instance is started.
- The relay is unsupervised; a crash surfaces as request failures.

## Security

- The host proxy listens on the gateway IP only, never `0.0.0.0`.
- The guest relay listens on loopback only and never terminates TLS.
- Unknown source IP or unmatched host/path → `403`; token mismatch → `401`;
  locked vault → `503`. There is no open-proxy behavior.
- The CA private key and all real secrets are never logged.
- Streaming timeouts are generous to avoid truncating long completions.

## Configuration

| Flag | Default | Purpose |
| --- | --- | --- |
| `--proxy-port` | `8443` | TLS listener port on the gateway |
| `--proxy-http-port` | `8080` | plain listener port for `http://` mock URLs |
| `--no-proxy` | `false` | disable the bridge (no hijack, no relay) |

## Spec Divergence

`BACKEND.md` §2.2 describes `--add-host <host>:172.28.0.1` plus port-`443`
interception or `iptables` `REDIRECT`. Because the daemon is unprivileged,
Vivarium instead hijacks to `127.0.0.1` and runs a guest-side relay to the
unprivileged gateway ports, achieving the same transparent behavior without
root. The host entries are applied to `/etc/hosts` at runtime (rather than via
`--add-host`) so endpoints can be rebound live.
