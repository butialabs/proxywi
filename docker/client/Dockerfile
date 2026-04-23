FROM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum* ./

RUN go mod download

COPY cmd/proxywi-client  ./cmd/proxywi-client
COPY internal/config      ./internal/config
COPY internal/client      ./internal/client
COPY internal/tunnel      ./internal/tunnel

ENV CGO_ENABLED=0

RUN go build -trimpath -ldflags="-s -w" -o /out/proxywi-client ./cmd/proxywi-client

FROM gcr.io/distroless/static:nonroot

COPY --from=build /out/proxywi-client /proxywi-client

USER nonroot:nonroot

ENTRYPOINT ["/proxywi-client"]