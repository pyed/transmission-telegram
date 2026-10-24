# CLAUDE.md

Guidance for working on transmission-telegram, a Telegram bot that controls a
Transmission BitTorrent daemon over its RPC API. If a `HANDOFF.md` exists in the
repository root (it is local and never committed), read it first: it records the
work in progress and this machine's quirks.

The project is public and has users. Keep command names, aliases, flags and
environment variables working unless a change has been agreed, and say in the
README how to upgrade when one is.

## Layout

The module is `github.com/pyed/transmission-telegram/v2` (the `/v2` suffix is
what makes the v2 tags installable), with the main package at the root, so
`go install github.com/pyed/transmission-telegram/v2@latest` builds a binary
named `transmission-telegram`.

| Path | What it holds |
|---|---|
| `main.go` | Entry point: config, logger, Transmission client, bot, completion watcher |
| `internal/redact` | `URL` and `Error` keep credentials and query tokens out of logged URLs and request errors |
| `internal/config` | Flags and environment (`ParseFlags`), `MasterSlice` (authorized users), `Version` |
| `internal/bot/bot.go` | `Bot`, `New` (startup calls to Telegram, retried), `request`, `reply`, update dispatch (`dispatch`, `serveChat`, `handleUpdate`), panic recovery, `SendHTML`/`OpenButton` for the watcher |
| `internal/bot/buttons.go` | The button router (`handleCallbackQuery`): verbs, v2.x `cmd:` buttons (`legacyPress`), `pressedTorrent`, `editPressed`, `answerCallback` |
| `internal/bot/card.go` | The torrent card: `cardFields`, `cardKeyboard`, actions (`a:`), removal (`y:`/`n:`), `sendCard`, `torrentFingerprint` |
| `internal/bot/screens.go` | Paged lists: `listing`, `listPage`, `replyList`, and the `screenStore` of the last 500 list and question messages (`pg:`, `back`) |
| `internal/bot/files.go` | `/files` and its buttons (`fl:`, `fo:`, `fs:`, `fa:`, `c:`) |
| `internal/bot/commands.go` | `commandTable`: every command's name, aliases, arguments and help; dispatch, `/help` and the Telegram menu are generated from it |
| `internal/bot/telegram.go` | Sending: HTML helpers (`esc`, `plainText`), UTF-16 lengths, chunking between lines, the `.txt` fallback, `retryTelegram`, `edit` |
| `internal/bot/format.go` | How torrents look: `torrentLine` (lists), `torrentBlock` (head, tail, active), `torrentCard` (info), icons, sizes, rates |
| `internal/bot/lists.go`, `info.go`, `session.go`, `settings.go`, `actions.go` | Commands by what they do |
| `internal/bot/live.go` | Live edits of the head, tail, active, speed and info replies |
| `internal/notify` | The `Watcher`: checks Transmission every `-watch-interval` and announces completions, outages, new errors and low disk space; `Kinds`, `Chats` and `KindsOf` say which chats get what. `-transmission-logfile` is accepted but ignored |
| `internal/state` | The state file (`-state`/`TT_STATE`): `Data` is everything the bot remembers (last chat, `/notify` choices, sorts, incomplete torrents, menu); `Store.Update` saves atomically, 0600 |
| `internal/bot/notify.go`, `sorting.go` | `/notify`; each chat's sort order and `b.torrents` |
| `internal/logfile` | `-logfile`: a 0600 log file moved aside to `FILE.1` at 10 MiB |

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
  `redact.URL` and request errors through `redact.Error`; configuration errors
  never repeat the URL. Telegram file URLs contain the token, so they are fetched
  in-process (`downloadTelegramFile`) and their errors are wrapped in
  `privateDownloadError`. Torrents uploaded in chat reach Transmission as
  metainfo, never as a URL.
- Replies are HTML. Put everything that is not the bot's own markup (names,
  paths, errors, what users typed) through `esc`, and keep every tag within one
  line: long replies are split between lines. `TestRepliesAreValidHTMLAndShowNamesExactly`
  runs every command against hostile names; add new commands to it.
- A new command goes in `commandTable` only; the menu, `/help`, and dispatch
  follow. Keep old names and aliases (`TestCommandsKeepTheirNamesAndAliases`).
- Authorization is by user: `Config.Masters.Contains(username, userID)` for
  messages, and the same check on `cb.From` for button presses.
- Buttons: callback data is `<verb>:<arguments>`, at most 64 bytes, and names
  a torrent by ID and fingerprint (the first 16 characters of its info-hash,
  `torrentFingerprint`), so an old button never acts on another torrent that
  came to have its ID. Every press is answered, and only masters may press.
  Card (`a:`, `y:`, `n:`, `t:`, `c:`) and file (`fl:`, `fo:`, `fs:`, `fa:`)
  buttons carry all they need and work after restarts; `pg:`, `back` and
  `dd:` (the `/deldata` question) need the message in the `screenStore`, in
  memory, and say they are too old when it is gone. Removing data always asks
  first. A press ends the pressed card's live view (`stopLive`, which waits for
  an edit under way). The `cmd:` buttons of v2.x cards still work.
- List commands make a `listing` in `b.listing`, which page buttons run again,
  so pages show fresh data; add a new list there and in `TestEveryListCommandHasPages`.
- Updates go through `dispatch`: the library hands them over one at a time
  (`WithNotAsyncHandlers`), and each master chat gets a queue and a goroutine
  of its own (`serveChat`), so a chat's commands run in order and chats run
  side by side. Shared state needs a lock. Background work goes through
  `b.launch`, which recovers panics and is counted in `b.wg` for shutdown;
  never block a chat's goroutine for long (live views run in the background
  through `startLive`, one per chat).
- Anything the bot must remember across restarts is a field of `state.Data`;
  change it with `Store.Update`, and only when it changed (every update writes
  and syncs the file). A save that fails keeps the change in memory and is
  logged, never fatal.
- Lists go through `b.torrents(ctx, chatID)`, which applies the chat's saved
  sort (stable, ties by ID); don't use the client's global `SetSort`.
- The watcher knows torrents by info-hash (IDs change when Transmission
  restarts), asks only for `watchFields`, and is tested through `check` with a
  fake clock and an in-memory `fakeTransmission` (`internal/notify`). Until a
  chat uses `/notify`, completions and outages go to `state.Data.LastChat`.
- Actions on several torrents look them up in one request
  (`torrentsNamed`), act in one more, and reply once (`summary`).
- Tests drive handlers directly against in-process fakes: `newFakeTelegram`
  (`internal/bot/testhelpers_test.go`: `script` scripts a method's answers,
  `callCount`, `sentTexts`/`sentChats`/`sentParseModes`/`sentDocuments`,
  `editsOf`, `waitForSent`) and `newFakeTransmission`
  (`internal/bot/actions_test.go`: `torrents`, `session`, `stats`,
  `addAnswer`, `stall`; `torrent-get` honours requested IDs). `req(args...)`
  is a command in chat 42, `message`/`messageIn` an update from master 12345.
  Add a regression test with every fix, and prove new tests can fail by
  breaking the code they guard.
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
  commit, builds archives for 11 platforms with checksums, publishes a GitHub
  release, and then pushes a five-platform image to
  `ghcr.io/pyed/transmission-telegram` (tags `X.Y.Z`, `X.Y`, `vX.Y.Z`,
  `latest`). CI builds the same five platforms on every push. Never re-create a
  pushed tag; bump the patch version instead.
- Go versions: `go.mod` says `go 1.26.0` with `toolchain go1.26.10`; CI tests
  Go 1.26.10 and 1.27.3, and releases and the Docker build use 1.27.3. When
  govulncheck reports a fixed standard-library vulnerability, move the
  toolchain line, the CI matrix, `release.yml`, and the Dockerfile's `golang`
  image together.
- The Dockerfile's runtime stage must stay free of `RUN` steps, so buildx can
  build every platform without emulation.
- Dependabot opens monthly grouped updates for Go modules, Actions, and the
  Docker base images.
