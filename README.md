# Proxywi 🥝

[![Docker Image](https://img.shields.io/badge/docker-proxywi-blue.svg)](https://github.com/butialabs/proxywi)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)
![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/butialabs/proxywi)

Is a cloud proxy pool that lets you route HTTP(S) traffic through a fleet of agents you control. Residential boxes, VPS instances, Raspberry Pis (x64), anything that can run Docker. A single public endpoint handles authentication and picks an agent at random for every new connection, so the outbound IP rotates on its own.

It is the "bring-your-own-IP" proxy setup most scraping and privacy tools assume you already have, packaged as two Docker images and a web GUI.

---

## How it works

```
                         proxywi.example.com
        ┌──────────────────────────────────────────────────────────────┐
        │  Caddy (caddy-l4)               proxywi-server                │
        │  :443  GUI + wss   ───────────► :3000  Admin UI + WS control   │
        │  :8443 HTTPS proxy ──TLS──────► :8080  HTTP forward proxy      │
        │  :1080 SOCKS5      ───────────► :11080 SOCKS5 proxy            │
        │  :80   ACME                     (PROXY protocol → real IP)     │
        └──────────────────────────────────────────────────────────────┘
                 ▲ wss (agents dial out)        ▲ yamux stream per
                 │                              │ proxied connection
        ┌────────┴───────────────┬─────────────┴────────┐
     Agent A                  Agent B               Agent C
   (home, BR)                (VPS, DE)              …
```

- The **server** ships as a single all-in-one Docker image!
- **Agents** run the client image. They dial *out* over `wss://` to `/ws/control` (so they work behind NAT/firewall without port-forwarding), finish a **token-only** handshake, and multiplex proxy streams over the tunnel with **yamux**
- When someone hits `user:pass@proxywi.example.com:8443` (HTTPS proxy) or `user:pass@proxywi.example.com:1080` (SOCKS5), the server authenticates them, picks a random online agent, opens a new yamux stream to that agent over the long-lived control tunnel

---

## Quick start

### 1. Run the server

```bash
nano compose.yml
```
```bash
services:
  proxywi-server:
    container_name: proxywi-server
    image: ghcr.io/butialabs/proxywi-server:latest
    restart: unless-stopped
    environment:
      PROXYWI_DOMAIN: "proxy.example.com"
      ACME_EMAIL: "email@example.com"
    volumes:
      - ./proxywi/data:/data
    ports:
      - "80:80"
      - "443:443"
      - "443:443/udp"
      - "8443:8443"
      - "1080:1080"
    networks:
      - proxywi-server

networks:
  proxywi-server:
    name: proxywi-server
    driver: bridge
```
```bash
docker compose -f compose.yml up -d
```

### 2. Enroll an agent

In the GUI, go to **Clients → Add client**. The agent gets an automatic 3-word name; the server shows the token *once* and offers a `docker-compose.yml` tailored to that client

### 3. Create a proxy user

**Proxy Access → New access**!

### 4. Use the proxy

```bash
# HTTP forward proxy over TLS (HTTPS proxy)
curl -x https://user:pass@proxywi.example.com:8443 https://ifconfig.me/ip

# SOCKS5
curl -x socks5h://user:pass@proxywi.example.com:1080 https://ifconfig.me/ip
```

Run it 10 times in a loop: each call exits through a different agent.

---

## Configuration

### Server

| Variable                    | Default              | Purpose |
|-----------------------------|----------------|---------|
| `PROXYWI_DOMAIN`            | *required*     | Public host. Used by the bundled Caddy for automatic HTTPS and by the GUI. Set this in `.env`. |
| `ACME_EMAIL`               | *required*      | Let's Encrypt contact for the bundled Caddy. Set this in `.env`. |
| `PROXYWI_PORT`              | `3000`         | Admin GUI + WS control plane listener (internal; Caddy publishes `443`) |
| `PROXYWI_PROXY_HTTP_PORT`   | `8080`         | HTTP forward-proxy listener (internal; Caddy publishes `8443` over TLS) |
| `PROXYWI_PROXY_SOCKET_PORT` | `11080`        | SOCKS5 listener (internal; Caddy publishes `1080`) |
| `PROXYWI_PROXY_PROTOCOL`    | `true`         | Accept the PROXY protocol on the proxy listeners so the real client IP survives the bundled Caddy. Only enable behind a trusted L4 terminator. |
| `PROXYWI_DATA_DIR`          | `./data`       | Where SQLite data lives |
| `ADMIN_USERNAME`            | *required*     | Initial administrator username. Created on first start if no admin exists. |
| `ADMIN_PASSWORD`            | *required*     | Initial administrator password (min. 8 characters). |

> The image ships with the proxy listeners on internal ports and `PROXYWI_PROXY_PROTOCOL=true`; you normally only set `PROXYWI_DOMAIN` and `ACME_EMAIL`. Published ports: **443** (GUI/wss), **8443** (HTTPS proxy), **1080** (SOCKS5), **80** (ACME).

### Client / agent

| Variable                | Default    | Purpose |
|-------------------------|------------|---------|
| `PROXYWI_SERVER`        | *required* | e.g. `ws://proxywi.xyz:3000` or `wss://proxywi.xyz` when a TLS-terminating reverse proxy is in front. Points at `PROXYWI_DOMAIN`. |
| `PROXYWI_TOKEN`         | *required* | Token from the GUI, shown once at enrollment. This is the only credential the agent needs. |
| `PROXYWI_TLS_INSECURE`  | `false`    | Skip TLS verification when dialing the control plane over `wss://` behind a self-signed reverse proxy. |

---

**Made with ❤️ by [Butiá Labs](https://butialabs.com)**
