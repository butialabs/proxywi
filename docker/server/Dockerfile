FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY cmd/proxywi-server  ./cmd/proxywi-server
COPY internal/config      ./internal/config
COPY internal/i18n        ./internal/i18n
COPY internal/server      ./internal/server
COPY internal/storage     ./internal/storage
COPY internal/tunnel      ./internal/tunnel
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags="-s -w" -o /out/proxywi-server ./cmd/proxywi-server

FROM caddy:2-builder AS caddy-build
RUN xcaddy build --with github.com/mholt/caddy-l4

FROM alpine:3.20
RUN apk add --no-cache ca-certificates

COPY --from=build       /out/proxywi-server /usr/bin/proxywi-server
COPY --from=caddy-build /usr/bin/caddy      /usr/bin/caddy
COPY docker/server/Caddyfile     /etc/caddy/Caddyfile
COPY docker/server/entrypoint.sh /entrypoint.sh
RUN sed -i 's/\r$//' /entrypoint.sh && chmod +x /entrypoint.sh && mkdir -p /data /config

ENV PROXYWI_DATA_DIR=/data \
    XDG_DATA_HOME=/data \
    XDG_CONFIG_HOME=/config \
    PROXYWI_PORT=3000 \
    PROXYWI_PROXY_HTTP_PORT=8080 \
    PROXYWI_PROXY_SOCKET_PORT=11080 \
    PROXYWI_PROXY_PROTOCOL=true

VOLUME ["/data"]

EXPOSE 80 443 8443 1080

ENTRYPOINT ["/entrypoint.sh"]
