FROM golang:1.11

WORKDIR /src/github.com/pyed/transmission-telegram

COPY ./ /src/github.com/pyed/transmission-telegram/
RUN go get -d -v
RUN CGO_ENABLED=0 GOOS=linux go build

FROM alpine:3.8

RUN apk add --no-cache ca-certificates && \
    update-ca-certificates

COPY --from=0 /src/github.com/pyed/transmission-telegram/transmission-telegram /usr/bin/transmission-telegram

ENTRYPOINT ["transmission-telegram"]
