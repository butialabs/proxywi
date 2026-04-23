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
RUN mkdir -p /out/data && chown -R 65532:65532 /out/data

FROM gcr.io/distroless/static:nonroot

COPY --from=build /out/proxywi-server /proxywi-server
COPY --from=build --chown=65532:65532 /out/data /data

USER nonroot:nonroot

ENV PROXYWI_DATA_DIR=/data

VOLUME ["/data"]

EXPOSE 8080 1080 3000

ENTRYPOINT ["/proxywi-server"]