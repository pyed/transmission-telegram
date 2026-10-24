ARG VERSION=dev

FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine AS build

ARG TARGETOS TARGETARCH TARGETVARIANT
ARG VERSION
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Everything runs on the build platform: Go cross-compiles, and time zones are
# compiled in (timetzdata), so the runtime stage needs no package installs and
# every platform builds without emulation.
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} GOARM=${TARGETVARIANT#v} \
    go build -trimpath -tags timetzdata \
    -ldflags="-s -w -X github.com/pyed/transmission-telegram/v2/internal/config.Version=${VERSION}" \
    -o /transmission-telegram . \
 && mkdir -p /rootfs/config

FROM alpine:3.24
ARG VERSION
LABEL org.opencontainers.image.source="https://github.com/pyed/transmission-telegram" \
      org.opencontainers.image.description="Manage and monitor Transmission through Telegram" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.version="${VERSION}"
# Alpine's base image already includes the CA bundle (ca-certificates-bundle).
COPY --from=build --chown=1000:1000 /rootfs/config /config
COPY --from=build /transmission-telegram /transmission-telegram

# What the bot remembers, such as where notifications go; mount a volume
# at /config to keep it when the container is replaced.
ENV TT_STATE=/config/state.json

USER 1000:1000

ENTRYPOINT ["/transmission-telegram"]
