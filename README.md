# Proxywi 🥝

[![Docker Image](https://img.shields.io/badge/docker-proxywi-blue.svg)](https://github.com/butialabs/proxywi)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)
![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/butialabs/proxywi)

A cloud proxy pool that routes HTTP(S) and SOCKS5 traffic through a fleet of agents you control, residential boxes, VPS instances, Raspberry Pis, anything that can run Docker. A single public endpoint handles authentication and picks an agent for every new connection, so the outbound IP rotates automatically.

Bring-your-own-IP proxy setup packaged as two Docker images and a web GUI.

---

## How it works

```
                         proxywi.example.com
        ┌─────────────────────┐    ┌──────────────────────────────────────┐
        │   proxywi-traefik   │    │          proxywi-server              │
        │  :443  GUI + wss  ──┼───►│  :3000  Admin UI + WS control        │
        │  :8443 HTTPS proxy ─┼───►│  :8080  HTTP forward proxy           │
        │  :1080 SOCKS5      ─┼───►│  :1080  SOCKS5 proxy                 │
        │  :80   ACME (HTTP)  │    │                                      │
        └─────────────────────┘    └──────────────────────────────────────┘
                 ▲ wss (agents dial out)        ▲ yamux stream per
                 │                              │ proxied connection
        ┌────────┴───────────────┬─────────────┴────────┐
     Agent A                  Agent B               Agent C
   (home, BR)                (VPS, DE)              …
```

- **`proxywi-traefik`** handles TLS termination via Let's Encrypt, HTTP-HTTPS redirect, PROXY protocol forwarding, and TCP passthrough for SOCKS5/HTTPS proxy ports.
- **`proxywi-server`** runs the proxy logic and admin GUI.
- **Agents** run the client image. They dial *out* over `wss://` to `/ws/control` (works behind NAT/firewall, no port-forwarding needed), authenticate with a token, and multiplex proxy streams over the tunnel using **yamux**.
- When a client hits `user:pass@proxywi.example.com:8443` or `:1080`, the server authenticates, picks a random online agent, opens a new yamux stream over the long-lived control tunnel, and proxies the connection.

---

## Quick start

### 1. Run the server

Create a `.env` file:

```bash
PROXYWI_DOMAIN=proxy.example.com
ACME_EMAIL=email@example.com
ADMIN_USERNAME=admin
ADMIN_PASSWORD=change-me
```

Create `compose.yml`:

```yaml
services:
  proxywi-traefik:
    container_name: proxywi-traefik
    image: traefik:v3
    restart: unless-stopped
    environment:
      TZ: "UTC"
    command:
      - "--providers.docker=true"
      - "--providers.docker.exposedbydefault=false"
      - "--providers.docker.network=proxywi"
      - "--entrypoints.web.address=:80"
      - "--entrypoints.web.http.redirections.entrypoint.to=websecure"
      - "--entrypoints.web.http.redirections.entrypoint.scheme=https"
      - "--entrypoints.websecure.address=:443"
      - "--entrypoints.socks.address=:1080"
      - "--entrypoints.httpsproxy.address=:8443"
      - "--certificatesresolvers.le.acme.email=${ACME_EMAIL}"
      - "--certificatesresolvers.le.acme.storage=/data/acme.json"
      - "--certificatesresolvers.le.acme.tlschallenge=true"
    ports:
      - "80:80"
      - "443:443"
      - "1080:1080"
      - "8443:8443"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - ./proxywi/traefik:/data
    networks:
      - proxywi

  proxywi-server:
    container_name: proxywi-server
    image: ghcr.io/butialabs/proxywi-server:latest
    restart: unless-stopped
    environment:
      TZ: "UTC"
      ADMIN_USERNAME: "${ADMIN_USERNAME}"
      ADMIN_PASSWORD: "${ADMIN_PASSWORD}"
      PROXYWI_DOMAIN: "${PROXYWI_DOMAIN}"
    volumes:
      - ./proxywi/data:/data
    networks:
      - proxywi
    labels:
      - "traefik.enable=true"
      - "traefik.http.routers.gui.rule=Host(`${PROXYWI_DOMAIN}`)"
      - "traefik.http.routers.gui.entrypoints=websecure"
      - "traefik.http.routers.gui.tls.certresolver=le"
      - "traefik.http.services.gui.loadbalancer.server.port=3000"
      - "traefik.tcp.routers.socks.rule=HostSNI(`*`)"
      - "traefik.tcp.routers.socks.entrypoints=socks"
      - "traefik.tcp.services.socks.loadbalancer.server.port=1080"
      - "traefik.tcp.services.socks.loadbalancer.proxyprotocol.version=2"
      - "traefik.tcp.routers.httpsproxy.rule=HostSNI(`${PROXYWI_DOMAIN}`)"
      - "traefik.tcp.routers.httpsproxy.entrypoints=httpsproxy"
      - "traefik.tcp.routers.httpsproxy.tls=true"
      - "traefik.tcp.routers.httpsproxy.tls.certresolver=le"
      - "traefik.tcp.services.httpsproxy.loadbalancer.server.port=8080"
      - "traefik.tcp.services.httpsproxy.loadbalancer.proxyprotocol.version=2"

networks:
  proxywi:
    name: proxywi
    driver: bridge
```

```bash
docker compose up -d
```

### 2. Enroll an agent

In the GUI go to **Clients > Add client**. The agent gets a random 3-word name. The server shows the token once and offers a ready-to-use `docker-compose.yml` for that agent.

### 3. Create a proxy user

**Proxy Access > New access**

### 4. Use the proxy

```bash
# SOCKS5
curl --socks5 user:pass@proxywi.example.com:1080 https://ifconfig.me/ip

# HTTP CONNECT proxy over TLS
curl -x https://user:pass@proxywi.example.com:8443 https://ifconfig.me/ip
```

Run it 10 times in a loop, each call exits through a different agent, different IP.

---

## Configuration

### proxywi-server

| Variable                    | Default                                       | Purpose |
|-----------------------------|-----------------------------------------------|---------|
| `PROXYWI_PORT`              | `3000`                                        | Admin GUI and WS control plane. |
| `PROXYWI_PROXY_HTTP_PORT`   | `8080`                                        | HTTP forward-proxy listener (Traefik publishes `:8443`) |
| `PROXYWI_PROXY_SOCKET_PORT` | `1080`                                        | SOCKS5 listener. |
| `PROXYWI_PROXY_PROTOCOL`    | `false`                                       | Accept PROXY protocol on proxy listeners (set `true` behind Traefik to get real client IPs) |
| `PROXYWI_DATA_DIR`          | `./data`                                      | SQLite database directory. |
| `ADMIN_USERNAME`            | *required*                                    | Initial admin username |
| `ADMIN_PASSWORD`            | *required*                                    | Initial admin password (min 8 chars) |
| `PROXYWI_DOMAIN`            | *(unset)*                                     | Public hostname, used for PROXY protocol IP resolution. |
| `PROXYWI_RATE_LIMIT_MAX`    | `5`                                           | Max proxy requests per user per window |
| `PROXYWI_RATE_LIMIT_WINDOW` | `1s`                                          | Rate-limit window (`500ms`, `2s`, `1m`, etc.) |
| `PROXYWI_BLOCK_CATEGORIES`  | *(unset)*                                     | Comma-separated domain categories to block: `adult`, `gambling`, `crypto`. Lists fetched on startup and refreshed weekly |

### proxywi-client (agent)

| Variable                  | Default                                       | Purpose |
|---------------------------|-----------------------------------------------|---------|
| `PROXYWI_SERVER`          | *required*                                    | Control plane URL, e.g. `wss://proxywi.example.com`. |
| `PROXYWI_TOKEN`           | *required*                                    | Agent token from the GUI. Shown once at enrollment. |

---

**Made with ❤️ by [Butiá Labs](https://butialabs.com)**
