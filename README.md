# transmission-telegram

[![CI](https://github.com/pyed/transmission-telegram/actions/workflows/ci.yml/badge.svg)](https://github.com/pyed/transmission-telegram/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/pyed/transmission-telegram)](https://goreportcard.com/report/github.com/pyed/transmission-telegram)
[![Release](https://img.shields.io/github/v/release/pyed/transmission-telegram)](https://github.com/pyed/transmission-telegram/releases)
[![License: Apache-2.0](https://img.shields.io/badge/License-Apache--2.0-blue.svg)](LICENSE)

### Manage and monitor your Transmission BitTorrent client through Telegram.

<img src="https://raw.github.com/pyed/transmission-telegram/master/demo.gif" width="450" />

---

## Key Features

- ⚡ **Modern Telegram Bot Engine**: Built with [`github.com/go-telegram/bot`](https://github.com/go-telegram/bot) with native graceful shutdown, context propagation, and structured `log/slog` logging.
- 🎛 **Interactive Buttons**: Lists come ten torrents a page, with a button for each torrent that opens its card; cards pause, resume, verify, reannounce, and remove torrents, with or without their data (asking first); `/files` skips files or changes their priority; notifications open the torrent's card.
- 📊 **Visual Progress Bars**: Clean Unicode progress bars (`[█████░░░░░] 50.0%`) on `/head`, `/tail`, `/active`, `/paused`, `/checking`, and `/info`.
- 🔔 **Operational Alerts**: Receive completion notifications plus one-shot Transmission outage and recovery alerts by watching Transmission over RPC, which works with Transmission running anywhere, Docker included.
- 🐢 **Turtle Mode & Utilities**: Instant speed toggling (`/turtle`), free disk space checker (`/free`), and manual tracker re-announcing (`/reannounce`).
- 🔒 **Flexible Authorization**: Restrict access with a repeatable `-master` list supporting Telegram usernames and stable numeric user IDs.
- 👥 **Group Chat Ready**: Clean handling of group mentions (e.g. `/list@BotName`).
- 📱 **Telegram Menu Auto-Registration**: Automatically configures the Telegram command suggestion menu on startup.

---

## Installation

### Pre-compiled Binaries

Download an archive for Linux, macOS, Windows, or FreeBSD from the [Releases](https://github.com/pyed/transmission-telegram/releases) page. Each release includes SHA-256 checksums.

### From Source (Go 1.26+)

Go 1.26 or newer is required; an older 1.26 release downloads Go 1.26.10, the first without known vulnerabilities that affect the bot.

```bash
go install github.com/pyed/transmission-telegram/v2@latest
```

---

## Configuration

`transmission-telegram` is configured via command-line flags or environment variables:

| Flag | Environment Variable | Description |
|---|---|---|
| `-token` | `TT_BOT_TOKEN` (`TT_BOTT` legacy) | Telegram bot token (*required*) |
| `-master` | `TT_MASTERS` (comma-separated) | Authorized Telegram user ID or username (*required*, repeatable); `1` alone is a setup mode that tells whoever writes to the bot their user ID |
| `-url` | `TR_URL` | Transmission RPC URL (default: `http://localhost:9091/transmission/rpc`) |
| `-username` | `TR_AUTH` (`user:password`) | Transmission RPC username |
| `-password` | `TR_AUTH` (`user:password`) | Transmission RPC password |
| `-logfile` | — | Send bot logs to a file only its owner can read, moved aside to `FILE.1` at 10 MiB (default: stdout) |
| `-transmission-logfile` | — | Deprecated and ignored: Transmission 4 no longer logs completions at its default level, so notifications always come from RPC |
| `-no-live` | — | Disable real-time editing and live update loops |
| `-state` | `TT_STATE` | File where the bot keeps what it remembers: where notifications go, each chat's sort order, status message and digest, which torrents were not done (default: `transmission-telegram/state.json` in the user's config directory; `/config/state.json` in the Docker image) |
| `-watch-interval` | — | How often to check Transmission for notifications (default: `10s`) |
| `-low-disk` | — | Free space below which chats that chose `disk` notifications are warned (default: `5GB`; `0` turns it off) |
| `-version` | — | Print the version and exit |

Explicit flags override the corresponding environment defaults. Prefer numeric Telegram user IDs for `-master`; unlike usernames, they cannot be renamed or reassigned.

### Notifications

The bot watches Transmission over RPC and tells chats about:

- **completed**: torrents that finish downloading, including those that finished while the bot was not running; more than three at once come in one message.
- **outages**: Transmission not answering three checks in a row, once, and answering again.
- **errors**: torrents that get a new tracker or local error (not passing tracker warnings), grouped by message.
- **disk**: free space where Transmission downloads falling below `-low-disk`, once until it recovers.

Send `/notify` in a chat to see what it gets, and `/notify on`, `/notify off`, or `/notify <kind> on|off` to change it. Until any chat chooses, completions and outages go to the chat a master used last, as in earlier versions; the bot remembers that chat across restarts in its state file. Notifications Telegram does not take at first are tried again for a day.

### Example CLI Usage

Load `TT_BOT_TOKEN` and, when needed, `TR_AUTH` from a protected environment file, service manager, or secret store before starting the bot. Avoid passing tokens and passwords as command-line flags on shared systems because process arguments and shell history may expose them.

```bash
transmission-telegram \
  -master="123456789" \
  -url="http://localhost:9091/transmission/rpc"
```

---

## Available Commands

| Command | Aliases | Description |
|---|---|---|
| `/list [query]` | `/li`, `/ls` | List all torrents (or filter by tracker query) |
| `/head [n]` | `/he` | Show the first *n* torrents with live speed updates (default: 5) |
| `/tail [n]` | `/ta` | Show the last *n* torrents with live speed updates (default: 5) |
| `/active` | `/ac` | List torrents actively uploading or downloading |
| `/downs` | `/dg` | List torrents currently downloading |
| `/seeding` | `/sd` | List torrents currently seeding |
| `/paused` | `/pa` | List paused torrents |
| `/checking` | `/ch` | List torrents verifying checksums |
| `/errors` | `/er` | List torrents with errors |
| `/latest [n]` | `/la` | List newest added torrents |
| `/search <query>` | `/se` | Search torrents by name |
| `/labels [label\|-]` | — | Count torrents per label, or list a label's torrents (`-`: those without one); Transmission 3.00+ |
| `/queue [top\|up\|down\|bottom <id...>]` | `/qu` | Show the download queue, or move torrents in it |
| `/info <id...>` | `/in` | Show details, progress bar, and interactive control buttons |
| `/files <id>` | `/fi` | List a torrent's files, ten to a page, to skip them or change their priority |
| `/add <url/magnet> [d=folder] [l=label] [paused]` | `/ad` | Add torrents by URL or magnet, optionally to a folder, with labels, or paused. Sending a `.torrent` file, or pasting a link, also adds it, with the same options in its caption or after the link |
| `/stop <id...\|all>` | `/sp` | Stop specific torrents or all |
| `/start <id...\|all>` | `/st` | Start specific torrents or all |
| `/check <id...\|all>` | `/ck` | Verify specific torrents or all |
| `/del <id...>` | `/rm` | Remove torrent from Transmission |
| `/move <id...> <folder>` | `/mv` | Move torrents' data to another folder |
| `/setlabel <id...> <label...\|->` | — | Set torrents' labels, or remove them with `-` |
| `/deldata <id...> [confirm]` | — | Remove torrents and delete their local files, after asking with buttons; `confirm` deletes at once |
| `/turtle [on\|off]` | `/alt`, `/tu` | Toggle or set alternative speed limits ("Turtle Mode"); `/turtle schedule HH:MM-HH:MM [days\|weekdays\|weekends]` or `off` sets Transmission's schedule for them, and `/turtle limits <down> <up>` their limits in KB/s |
| `/limit [down N] [up N] \| off` | — | Show both global limits and turtle mode, or set the limits in KB/s; `0`/`off` removes one |
| `/port` | — | Check whether peers can reach Transmission's port |
| `/free [path]` | `/space`, `/disk` | Show free and total disk space on the download drive |
| `/reannounce <id...\|all>` | `/ra` | Force tracker re-announce |
| `/digest [HH:MM [daily\|weekly\|monthly] \| now \| off]` | — | Get a summary every day, week (on Mondays), or month (on the 1st): what finished and was added, what Transmission transferred, torrents and their errors, and free space; `now` shows one at once. Times are in the bot's time zone (`TZ`) |
| `/status [off]` | — | Pin a message that shows speeds and limits, this session's totals, torrents at work, and free space, updated every minute and by its 🔄 button; `off` stops it |
| `/speed` | `/ss` | Display current download and upload speeds |
| `/stats` | `/sa` | Show Transmission session and cumulative statistics |
| `/downlimit [KB/s\|off]` | `/dl` | Show or set the global download speed limit; `0` or `off` removes it |
| `/uplimit [KB/s\|off]` | `/ul` | Show or set the global upload speed limit; `0` or `off` removes it |
| `/downloaddir <path>` | `/dd` | Set default download directory |
| `/sort <method>` | `/so` | Sort this chat's torrent listings (`id`, `name`, `age`, `size`, `progress`, `downspeed`, `upspeed`, `download`, `upload`, `ratio`; prefix with `rev` for reverse) |
| `/trackers` | `/tr` | List trackers and torrent counts |
| `/count` | `/co` | Show torrent counts grouped by status |
| `/notify [on\|off\|<kind> on\|off]` | — | Show or choose this chat's notifications (see [Notifications](#notifications)) |
| `/whoami` | — | Show your Telegram user ID and the chat's ID |
| `/help` | — | Display command help |
| `/version` | `/ver` | Display Transmission daemon and bot version |

---

## Docker

### Standalone
```bash
docker run -d --name transmission-telegram \
  --restart unless-stopped \
  --add-host=host.docker.internal:host-gateway \
  -e TT_BOT_TOKEN="<YOUR_BOT_TOKEN>" \
  -e TR_URL="http://host.docker.internal:9091/transmission/rpc" \
  -e TR_AUTH="<username>:<password>" \
  -e TZ="Etc/UTC" \
  -v transmission-telegram:/config \
  ghcr.io/pyed/transmission-telegram:latest \
  -master="<YOUR_NUMERIC_TELEGRAM_ID>"
```

The example connects to Transmission on the Docker host. When both applications run in Compose, use the Transmission service name instead. The `/config` volume keeps the bot's state file, such as where notifications go, when the container is replaced. Set `TZ` to your time zone, such as `Europe/Berlin`, for `/digest` and `/status` times.

### Docker Compose

Create a local `.env` file (never commit it):

```dotenv
TT_BOT_TOKEN=123456789:replace-with-your-token
TELEGRAM_MASTER_ID=123456789
TRANSMISSION_PASSWORD=replace-with-a-strong-password
```

Then use:

```yaml
services:
  transmission:
    image: lscr.io/linuxserver/transmission:latest
    container_name: transmission
    environment:
      PUID: 1000
      PGID: 1000
      TZ: Etc/UTC
      USER: transmission
      PASS: ${TRANSMISSION_PASSWORD}
    volumes:
      - /path/to/config:/config
      - /path/to/downloads:/downloads
    ports:
      - "9091:9091"
      - "51413:51413"
      - "51413:51413/udp"
    restart: unless-stopped

  telegram-bot:
    image: ghcr.io/pyed/transmission-telegram:latest
    container_name: transmission-telegram
    depends_on:
      - transmission
    restart: unless-stopped
    environment:
      TT_BOT_TOKEN: ${TT_BOT_TOKEN}
      TR_URL: http://transmission:9091/transmission/rpc
      TR_AUTH: transmission:${TRANSMISSION_PASSWORD}
      TZ: Etc/UTC
    volumes:
      - /path/to/bot-config:/config
    command: ["-master=${TELEGRAM_MASTER_ID}"]
```

The image runs as user `1000:1000`, so a directory mounted at `/config` must be writable by that user (`sudo chown 1000:1000 /path/to/bot-config`). Without a writable `/config`, the bot still runs, but forgets its settings when the container is replaced.

---

## Security

- Treat the Telegram bot token and Transmission credentials as secrets. Keep them in protected environment or secret-management facilities and never commit `.env` files.
- Prefer a numeric Telegram user ID in `-master`, especially for bots that can delete torrent data.
- Keep Transmission RPC on a trusted network or behind appropriate access controls; do not expose an unauthenticated endpoint to the public internet.
- Buttons name torrents by ID and info-hash, so an old button never acts on another torrent that came to have its ID. Removing a torrent's data always asks first, `/deldata` included; only `/deldata <id...> confirm`, meant for scripts, deletes at once, so use it carefully.

Please report vulnerabilities according to [SECURITY.md](SECURITY.md), not in a public issue.

## Development

See [CONTRIBUTING.md](CONTRIBUTING.md) for the local verification commands and contribution workflow.

---

## License

Licensed under the [Apache License 2.0](LICENSE).
