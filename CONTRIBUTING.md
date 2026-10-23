# Contributing

Thanks for helping improve transmission-telegram. For a substantial behavior change, open an issue first so the design and compatibility impact can be discussed. Security-sensitive reports belong in [SECURITY.md](SECURITY.md).

## Development setup

Install Git and Go 1.26 or newer, then clone the repository. The `toolchain` line in `go.mod` makes an older 1.26 release download the patched toolchain the project is tested with.

Before submitting a change, run:

```bash
go fmt ./...
go mod tidy
go mod verify
go vet ./...
go test ./...
```

On a Unix-like system with a C compiler, also run the race detector:

```bash
go test -race ./...
```

The CI vulnerability check can be reproduced with:

```bash
go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...
```

Do not commit generated binaries, coverage output, local `.env` files, bot tokens, RPC credentials, or private torrent data.

## Changes and tests

- Keep changes focused and preserve existing command aliases and configuration compatibility unless a breaking change is explicitly intended.
- Add regression tests for bug fixes and tests for new user-visible behavior.
- Propagate request contexts through network operations and avoid logging command payloads or credentials.
- Use clear, imperative commit subjects and explain non-obvious compatibility or security decisions in the commit body.

Pull requests should summarize user-visible effects, verification performed, and any deployment or upgrade considerations.
