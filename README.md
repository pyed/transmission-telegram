# transmission-telegram

#### Manage your transmission through Telegram.

<img src="https://raw.github.com/pyed/transmission-telegram/master/demo.gif" width="400" />

## CLI

###  Install

Just [download](https://github.com/pyed/transmission-telegram/releases) the appropriate binary for your OS, place `transmission-telegram` in your `$PATH` and you are good to go.

Or if you have `Go` installed: `go get -u github.com/pyed/transmission-telegram`

## Usage

[Wiki](https://github.com/pyed/transmission-telegram/wiki)


##  Docker Alternate Installation Route

### Standalone

```
docker run -d --name transmission-telegram \
kevinhalpin/transmission-telegram:latest \
-token=<Your Bot Token> \
-master=<Your Username> \
-url=<Transmission RPC> \
-username=<Transmission If Needed> \ 
-password=<Transmissions If Needed>
```

### docker-compose Example

```
version: '2.4'
services:
  transmission:
    container_name: transmission
    environment:
      - PUID=${PUID_DOCKUSER}
      - PGID=${PGID_APPZ}
    image: linuxserver/transmission
    network_mode: 'host'
    hostname: 'transmission'
    volumes:
      - ${CONFIG}/transmission:/config
      - ${DATA}/transmission/downloads:/downloads

telegram-transmission-bot:
    container_name: telegram-transmission-bot
    restart: on-failure
    depends_on:
      - transmission
      - plex
      - emby
    network_mode: 'host'
    image: kevinhalpin/transmission-telegram:latest
    command: '-token=${TELEGRAM_TRANSMISSION_BOT} -master=${TELEGRAM_USERNAME} -url=${TRANSMISSION_URL} -   username=${TRANSMISSION_USERNAME} -password=${PASS}'
```
