FROM golang:alpine as build

ENV GOOS=linux \
    GOARCH=amd64

RUN apk add --no-cache git

WORKDIR /go/src/transmission-telegram
COPY . .

RUN go mod init transmission-telegram
RUN go get -d -v ./...
RUN go install -v ./...

RUN go build -o main .
# RUN ls -lahR /go/

FROM alpine:latest as certs
RUN apk --update add ca-certificates

FROM bash:latest
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /go/bin/transmission-telegram /
RUN chmod 777 transmission-telegram

ENTRYPOINT ["/transmission-telegram"]