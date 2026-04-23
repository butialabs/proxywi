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
(GUI + WS control)              (HTTP / SOCKS5 proxy)
┌──────────────────────────┐    ┌────────────────────┐
│ :3000 Admin UI           │    │ :8080 HTTP proxy   │
│       /ws/control (WS)   │◄─┐ │ :1080 SOCKS5 proxy │
└──────────────────────────┘  │ └────────────────────┘
                              │           ▲
              reverse tunnel  │           │ yamux stream per
                              │           │ proxied connection
  ┌───────────────────────────┴───────────┴────────┐
Client A                  Client B              Client C
(home, BR)                (VPS, DE)             …
```

- The **server** runs as a single Go binary with three listeners:
  - `3000` serves the admin GUI and the WS control plane (`/ws/control`), this is where agents dial in.
  - `8080` is the HTTP forward proxy (`CONNECT` + absolute-URI).
  - `1080` is the SOCKS5 proxy (user/pass auth).
- All listeners speak plain HTTP/TCP. Terminate TLS in a reverse proxy (nginx, Caddy, Cloudflare, …) if you need `https://` / `wss://` in front.
- **Agents** run the same binary in client mode. They dial *out* to the server over WS/WSS (so they work behind NAT/firewall without port-forwarding), finish a token handshake, and multiplex proxy streams over the tunnel with [yamux](https://github.com/hashicorp/yamux).
- When somebody hits `user:pass@pomar.proxywi.xyz:8080` (HTTP) or `user:pass@pomar.proxywi.xyz:1080` (SOCKS5), the server authenticates them, picks a random online agent, opens a new yamux stream to that agent (over the long-lived control tunnel on `3000`), and pipes bytes. Every new connection can land on a different agent → IP rotation by default.
- Per-agent metrics (bytes in/out, active connections) stream back over a dedicated meta stream and are aggregated per-minute for the dashboard.

---

## Quick start

### 1. Run the server

```bash
curl -L https://raw.githubusercontent.com/butialabs/proxywi/refs/heads/main/compose-server.yml -o compose.yml
docker compose up -d
```

Open `http://proxywi.xyz:3000` (or whatever reverse proxy you put in front) in a browser, the first visit walks you through creating the admin user (username, email, password).

### 2. Enroll an agent

In the GUI, go to **Clients → Add client**, give it a name. The server shows the token *once* and offers a `docker-compose.yml` tailored to that client.

Within a few seconds the client shows as **online** in the dashboard.

### 3. Create a proxy user

**Users → New user**. Username, password, optional allowed tags (empty = may use any agent), optional allowed source CIDRs (empty = any caller).

### 4. Use the proxy

```bash
# HTTP forward proxy
curl -x http://user:pass@pomar.proxywi.xyz:8080 https://ifconfig.me/ip

# SOCKS5
curl -x socks5h://user:pass@pomar.proxywi.xyz:1080 https://ifconfig.me/ip
```

Run it 10 times in a loop: each call exits through a different agent.

---

## Configuration

### Server

| Variable                    | Default              | Purpose |
|-----------------------------|----------------------|---------|
| `PROXYWI_MAIN_DOMAIN`       | `proxywi.xyz`        | Public host of the GUI and the control plane agents connect to |
| `PROXYWI_MAIN_PORT`         | `3000`               | Admin GUI + WS control plane listener |
| `PROXYWI_PROXY_DOMAIN`      | `pomar.proxywi.xyz`  | Public host of the forward proxy |
| `PROXYWI_PROXY_HTTP_PORT`   | `8080`               | HTTP forward-proxy listener |
| `PROXYWI_PROXY_SOCKET_PORT` | `1080`               | SOCKS5 proxy listener |
| `PROXYWI_DATA_DIR`          | `./data`             | Where SQLite data lives |

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
| `PROXYWI_SERVER`        | *required* | e.g. `ws://proxywi.xyz:3000` or `wss://proxywi.xyz` when a TLS-terminating reverse proxy is in front. Points at `PROXYWI_MAIN_DOMAIN`, not the proxy domain. |
| `PROXYWI_TOKEN`         | *required* | Token from the GUI, shown once at enrollment |
| `PROXYWI_CLIENT_NAME`   | hostname   | Display name in the GUI |
| `PROXYWI_TLS_INSECURE`  | `false`    | Skip TLS verification when dialing the control plane over `wss://` behind a self-signed reverse proxy. |

---

## License

Proxywi is licensed under the **GNU Affero General Public License v3.0** (AGPL-3.0). See [LICENSE](LICENSE) for the full text.

In short: you are free to use, modify, and redistribute Proxywi, including for commercial purposes. However, if you run a modified version as a network service, you **must** make the complete corresponding source code of your modifications available to its users under the same license. This prevents closed-source commercial forks of the project.

---

**Made with ❤️ by [Butiá Labs](https://butialabs.com)**
