ARG VERSION=dev

FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine AS build

ARG TARGETOS TARGETARCH TARGETVARIANT
ARG VERSION
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} GOARM=${TARGETVARIANT#v} \
    go build -trimpath -ldflags="-s -w -X github.com/pyed/transmission-telegram/v2/internal/config.Version=${VERSION}" \
    -o /transmission-telegram .

FROM alpine:3.24
ARG VERSION
LABEL org.opencontainers.image.source="https://github.com/pyed/transmission-telegram" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.version="${VERSION}"
RUN apk --no-cache add ca-certificates tzdata
COPY --from=build /transmission-telegram /transmission-telegram

USER 1000:1000

ENTRYPOINT ["/transmission-telegram"]
