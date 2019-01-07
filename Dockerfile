FROM alpine:latest as certs
RUN apk --update add ca-certificates

FROM bash:latest
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY transmission-telegram /
RUN chmod 777 transmission-telegram

ENTRYPOINT ["/transmission-telegram"]