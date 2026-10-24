# Contributing

Thanks for helping improve transmission-telegram. For a substantial behavior change, open an issue first so the design and compatibility impact can be discussed. Security-sensitive reports belong in [SECURITY.md](SECURITY.md).

## Development setup

Install Git and Go 1.26 or newer, then clone the repository. The `toolchain` line in `go.mod` makes an older 1.26 release download the patched toolchain the project is tested with.

Before submitting a change, run:

```bash
gofmt -l .
go mod tidy -diff
go vet ./...
go run honnef.co/go/tools/cmd/staticcheck@v0.8.1 ./...
go test ./...
GOARCH=386 go test ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...
```

The 32-bit run matters because releases include 386 and 32-bit ARM builds. On a Unix-like system with a C compiler, also run the race detector:

```bash
go test -race ./...
```

## Against a real Transmission

The unit tests use in-process fakes of Telegram and Transmission. `TestAgainstTransmission` drives the bot against a real Transmission instead, and CI runs it against Transmission 4.0 and 4.1. It changes the daemon's settings and torrents, so run it against a throwaway one, such as:

```bash
docker run --rm -d --name tt-test -p 9091:9091 -e USER=ci -e PASS=ci-only-password lscr.io/linuxserver/transmission:latest
TT_TEST_TRANSMISSION_URL=http://localhost:9091/transmission/rpc TT_TEST_TRANSMISSION_AUTH=ci:ci-only-password \
  go test -count=1 -run TestAgainstTransmission ./internal/bot/
docker stop tt-test
```

## Changes and tests

- Keep changes focused, and keep existing commands, aliases, flags, and environment variables working unless a breaking change is agreed.
- Add a regression test with every fix, and tests for new user-visible behavior; a test should fail when the code it guards is broken.
- A new command goes in `commandTable` (`internal/bot/commands.go`), which `/help`, the Telegram menu, and the README's list of commands are made from. After changing it, rewrite the README's list with `go test ./internal/bot -run TestREADMEListsEveryCommand -update`.
- Replies are Telegram HTML: put names, paths, errors, and anything users typed through `esc`, and add new commands to `TestRepliesAreValidHTMLAndShowNamesExactly`.
- Pass request contexts to every network call, and never log or send the bot token, Transmission credentials, or command payloads.
- Do not commit binaries, coverage output, `.env` files, bot tokens, RPC credentials, or private torrent data.

Use clear, imperative commit subjects, and explain non-obvious compatibility or security decisions in the commit body. Pull requests should summarize user-visible effects, the verification performed, and any upgrade considerations.
