FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY cmd/proxywi-server  ./cmd/proxywi-server
COPY internal/config      ./internal/config
COPY internal/dns         ./internal/dns
COPY internal/i18n        ./internal/i18n
COPY internal/server      ./internal/server
COPY internal/storage     ./internal/storage
COPY internal/telemetry   ./internal/telemetry
COPY internal/tunnel      ./internal/tunnel
COPY internal/version     ./internal/version
ARG BUILD_VERSION=dev
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags="-s -w -X github.com/butialabs/proxywi/internal/version.Version=${BUILD_VERSION}" -o /out/proxywi-server ./cmd/proxywi-server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates && mkdir -p /data /config

COPY --from=build /out/proxywi-server /usr/bin/proxywi-server

ENV PROXYWI_DATA_DIR=/data \
    XDG_DATA_HOME=/data \
    XDG_CONFIG_HOME=/config \
    PROXYWI_PORT=3000 \
    PROXYWI_PROXY_HTTP_PORT=8080 \
    PROXYWI_PROXY_SOCKET_PORT=1080 \
    PROXYWI_PROXY_PROTOCOL=true

VOLUME ["/data"]

EXPOSE 3000 8080 1080

ENTRYPOINT ["/usr/bin/proxywi-server"]
