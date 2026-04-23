# Proxywi 🥝

[![Docker Image](https://img.shields.io/badge/docker-proxywi-blue.svg)](https://github.com/butialabs/proxywi)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)
![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/butialabs/proxywi)

Is a cloud proxy pool that lets you route HTTP(S) traffic through a fleet of agents you control. Residential boxes, VPS instances, Raspberry Pis, anything that can run Docker. A single public endpoint handles authentication and picks an agent at random for every new connection, so the outbound IP rotates on its own.

It is the "bring-your-own-IP" proxy setup most scraping and privacy tools assume you already have, packaged as two Docker images and a web GUI.

> Status: Stable beta.

---

## How it works

```
proxywi.xyz                     pomar.proxywi.xyz
(GUI + WSS control)             (HTTPS proxy)
┌──────────────────────────┐    ┌──────────────────┐
│ :3000 Admin UI           │    │ :7443 HTTPS proxy│
│       /ws/control (WSS)  │◄─┐ └──────────────────┘
└──────────────────────────┘  │           ▲
                              │           │ yamux stream per
                reverse tunnel│           │ proxied connection
                              │           │
  ┌───────────────────────────┴───────────┴────────┐
Client A                Client B              Client C
(home, BR)              (VPS, DE)             …
```

- The **server** runs as a single Go binary with two listeners:
  - `:3000` serves the admin GUI and the WSS control plane (`/ws/control`), this is where agents dial in.
  - `:7443` is the HTTPS forward proxy (`CONNECT`), dedicated to data-plane traffic.
- **Agents** run the same binary in client mode. They dial *out* to the server over WSS (so they work behind NAT/firewall without port-forwarding), finish a token handshake, and multiplex proxy streams over the tunnel with [yamux](https://github.com/hashicorp/yamux).
- When somebody hits `user:pass@pomar.proxywi.xyz:7443` via an HTTPS proxy, the server authenticates them, picks a random online agent, opens a new yamux stream to that agent (over the long-lived control tunnel on `:3000`), and pipes bytes. Every new connection can land on a different agent → IP rotation by default.
- Per-agent metrics (bytes in/out, active connections) stream back over a dedicated meta stream and are aggregated per-minute for the dashboard.

---

## Quick start

### 1. Run the server

```bash
curl -L https://raw.githubusercontent.com/butialabs/proxywi/refs/heads/main/compose-server.yml -o compose.yml
docker compose up -d
```

Two ports are exposed: `:3000` (admin GUI + WSS control) and `:7443` (HTTPS proxy).

`PROXYWI_TLS_MODE` controls TLS on the `:7443` proxy listener:

- **`on`** (default): Proxywi generates a long-lived self-signed cert on first boot, with SANs for the GUI domain, `pomar.proxywi.xyz`, `*.pomar.proxywi.xyz`, `localhost` and the loopback IPs. The cert/key are written to `PROXYWI_TLS_CACHE_DIR/self/`. Clients that don't trust self-signed certs will need to import the PEM into their trust store.
- **`off`**: Plain HTTP on both listeners. Put Caddy / nginx / Traefik in front as a TLS-terminating reverse proxy and forward `pomar.proxywi.xyz` → `:7443` (and `proxywi.xyz` → `:3000`).
- **`manual`**: Supply your own cert via `PROXYWI_TLS_CERT_FILE` + `PROXYWI_TLS_KEY_FILE`.
- **`autocert`**: Let's Encrypt,  requires port `80` reachable for the HTTP-01 challenge; proxywi starts a companion `:80` listener automatically in this mode.

`PROXYWI_TLS_MODE` applies to **both** listeners (`:3000` and `:7443`) with the same cert reused on both. With `off`, both are plain HTTP, front them with a reverse proxy that handles TLS + WebSocket upgrades (Caddy does both automatically; nginx needs the `Upgrade`/`Connection` headers).

Open `https://proxywi.xyz` in a browser,  the first visit walks you through creating the admin user (username, email, password).

### 2. Enroll an agent

In the GUI, go to **Clients → Add client**, give it a name. The server shows the token *once* and offers a `docker-compose.yml` tailored to that client.

Within a few seconds the client shows as **online** in the dashboard.

### 3. Create a proxy user

**Users → New user**. Username, password, optional allowed tags (empty = may use any agent), optional allowed source CIDRs (empty = any caller).

### 4. Use the proxy

```bash
# HTTPS proxy - TLS to the proxy, then CONNECT tunnel to the target
curl -k --proxy-insecure -x https://user:pass@pomar.proxywi.xyz:7443 https://ifconfig.me/ip
```

If you expose port `443` externally (reverse proxy on `443` → container `7443`):

```bash
curl -x https://user:pass@pomar.proxywi.xyz https://ifconfig.me/ip
```

Run it 10 times in a loop: each call exits through a different agent.

---

## Configuration

### Server

| Variable                  | Default                | Purpose |
|---------------------------|------------------------|---------|
| `PROXYWI_MAIN_DOMAIN`     | `proxywi.xyz`          | Public host of the GUI and the control plane and agents connect |
| `PROXYWI_MAIN_ADDR`       | `:3000`                | Admin GUI + WSS control plane listener |
| `PROXYWI_PROXY_DOMAIN`    | `pomar.proxywi.xyz`    | Public host of the forward proxy |
| `PROXYWI_PROXY_ADDR`      | `:7443`                | HTTPS forward-proxy listener (TLS-capable) |
| `PROXYWI_DATA_DIR`        | `./data`               | Where SQLite data lives |
| `PROXYWI_TLS_MODE`        | `on`                   | `on` (self-signed, default), `off` (reverse proxy does TLS), `manual`, `autocert` |
| `PROXYWI_TLS_CERT_FILE`   | -                      | PEM cert file (required when `TLS_MODE=manual`) |
| `PROXYWI_TLS_KEY_FILE`    | -                      | PEM key file (required when `TLS_MODE=manual`) |
| `PROXYWI_TLS_CACHE_DIR`   | `./data/acme`          | Cert cache directory (self-signed lives under `<dir>/self/`, autocert caches at the root) |
| `PROXYWI_ACME_EMAIL`      | -                      | Contact email for Let's Encrypt (optional) |

#### Data permissions

The server image is based on distroless and runs as UID/GID **65532**. If you bind-mount a host directory into `/data`, it must be writable by that user:

```bash
mkdir -p ./data
sudo chown -R 65532:65532 ./data
sudo chmod -R 755 ./data
```

### Client / agent

| Variable                | Default    | Purpose |
|-------------------------|------------|---------|
| `PROXYWI_SERVER`        | *required* | e.g. `wss://proxywi.xyz` points at `PROXYWI_MAIN_DOMAIN`, not the proxy domain. |
| `PROXYWI_TOKEN`         | *required* | Token from the GUI, shown once at enrollment |
| `PROXYWI_CLIENT_NAME`   | hostname   | Display name in the GUI |
| `PROXYWI_TLS_INSECURE`  | `false`    | Skip TLS verification when dialing the control plane. Required when the server is on `TLS_MODE=on` (self-signed) and the agent doesn't have the server cert in its trust store. |

---

## License

Proxywi is licensed under the **GNU Affero General Public License v3.0** (AGPL-3.0). See [LICENSE](LICENSE) for the full text.

In short: you are free to use, modify, and redistribute Proxywi, including for commercial purposes. However, if you run a modified version as a network service, you **must** make the complete corresponding source code of your modifications available to its users under the same license. This prevents closed-source commercial forks of the project.

---

**Made with ❤️ by [Butiá Labs](https://butialabs.com)**