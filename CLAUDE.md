# CLAUDE.md

Guidance for working on transmission-telegram, a Telegram bot that controls a
Transmission BitTorrent daemon over its RPC API. If a `HANDOFF.md` exists in the
repository root (it is local and never committed), read it first: it records the
work in progress and this machine's quirks.

The project is public and has users. Keep command names, aliases, flags and
environment variables working unless a change has been agreed, and say in the
README how to upgrade when one is.

## Layout

| Path | What it holds |
|---|---|
| `main.go` | Entry point: config, logger, Transmission client, bot, completion watcher; `safeEndpoint` keeps credentials out of logged URLs |
| `internal/config` | Flags and environment (`ParseFlags`), `MasterSlice` (authorized users), the `/help` text, `Version` |
| `internal/bot/bot.go` | `Bot`, startup calls to Telegram, update dispatch (`handleUpdate`), callback buttons, panic recovery |
| `internal/bot/handler.go` | The command map (names and aliases), `/help`, `/version` |
| `internal/bot/commands_*.go` | Commands by area: `list` (lists), `status` (state lists), `info` (card, stats, speed, count), `manage` (add, upload, del, start/stop/check), `settings` (sort, limits, turtle, free space, reannounce) |
| `internal/bot/send.go`, `liveupdate.go`, `format.go` | Sending and splitting messages, live edits, progress bars and tracker hosts |
| `internal/notify` | Completion notifications: the RPC watcher (`watcher.go`) and the legacy log tailer (`tailer.go`) |

Dependencies: `github.com/go-telegram/bot` (Telegram Bot API),
`github.com/pyed/transmission` (the RPC client, also maintained by pyed), and
`github.com/dustin/go-humanize`.

## Commands

```sh
go build ./... && go vet ./... && go test -count=1 ./...
GOARCH=386 go test -count=1 ./...          # 32-bit, as the 386/arm builds run
go run honnef.co/go/tools/cmd/staticcheck@v0.8.1 ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...
go mod tidy -diff
```

`go test -race` needs a C compiler; CI runs it on Linux. Always `set -o pipefail`
before piping test output into `tail` or `grep`, or a failure can hide.

## Conventions

- Every handler gets the update's `ctx`, and every RPC and Telegram call takes
  it, so shutdown cancels work in flight.
- Never log or send the bot token or Transmission credentials. URLs go through
  `safeEndpoint`; Telegram file URLs contain the token, so they are fetched
  in-process (`downloadTelegramFile`) and their errors are wrapped in
  `privateDownloadError`. Torrents uploaded in chat reach Transmission as
  metainfo, never as a URL.
- Authorization is by user: `Config.Masters.Contains(username, userID)` for
  messages, and the same check on `cb.From` for button presses.
- Button callback data is `cmd:<action>:<id>:<fingerprint>`, the fingerprint
  being the first 16 characters of the info-hash (`torrentFingerprint`), so an
  old button cannot act on a different torrent that reuses the ID. Destructive
  buttons ask for confirmation first.
- Tests drive handlers directly against in-process fakes: `newFakeTelegram`
  (`internal/bot/testhelpers_test.go`) and `newFakeTransmission`
  (`internal/bot/commands_manage_test.go`). Add a regression test with every
  fix, and prove new tests can fail by breaking the code they guard.
- Timing-based tests use generous margins and run `-count=30` once before
  committing.

## Transmission facts (verified in its source)

- Torrent status: 0 stopped, 1 check pending, 2 checking, 3 download pending,
  4 downloading, 5 seed pending, 6 seeding.
- RPC answers HTTP 409 with `X-Transmission-Session-Id` until a request carries
  that header; `pyed/transmission` retries once with it.
- `torrent-get` with `ids: "recently-active"` returns the torrents active in the
  last 60 seconds plus a `removed` list of IDs.
- `torrent-add` answers `torrent-duplicate` for a torrent it already has;
  `pyed/transmission`'s `AddTorrent` returns either as if added.
- Speed limits are KB/s; a limit of 0 with the limit enabled stalls transfers.
  Alternative-speed times are minutes after midnight, and its day field is a
  bitmask (Sunday 1 … Saturday 64).
- Labels need RPC version 16 (Transmission 3.00).
- Completion log lines: 3.x writes `NAME State changed from "Incomplete" to
  "Complete"` at info level, translated, with "Done" when some files are
  skipped. 4.x writes it unquoted at trace level, after a level token, so
  logfile-based notifications cannot work there.
- Transmission 4.1.0 (RPC 18, semver 6.0.0) added JSON-RPC 2.0 with snake_case
  names; the old protocol, which `pyed/transmission` speaks, is deprecated but
  still served.

## Telegram facts

- Message text is limited to 4,096 UTF-16 code units after entities are
  parsed; callback answers to 200; callback data to 64 bytes.
- With privacy mode on, a bot in a group sees only commands addressed to it and
  replies; with privacy mode off, or as an admin, it sees every message.
- go-telegram/bot runs every update's handler in its own goroutine unless
  `WithNotAsyncHandlers` is set.
- A private chat's ID is the user's ID.

## Git and releases

- Never commit, push, or tag without the user asking; branch first when on
  `master`, and never name a branch like a release tag.
- Commit messages: imperative subject with a conventional prefix (`fix:`,
  `feat:`, `build:`, `ci:`, `docs:`, `refactor:`, `test:`), then a body that
  explains the problem and the change. End with the `Co-Authored-By` trailer the
  system provides.
- Pushing a `v*` tag runs `.github/workflows/release.yml`: it verifies the
  commit, then builds archives for 11 platforms with checksums and publishes a
  GitHub release. Never re-create a pushed tag; bump the patch version instead.
- Dependabot opens monthly grouped updates for Go modules, Actions, and the
  Docker base images.
