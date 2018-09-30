# transmission-telegram

#### Manage your transmission through Telegram.

<img src="https://raw.github.com/pyed/transmission-telegram/master/demo.gif" width="400" />

## Install

Just [download](https://github.com/pyed/transmission-telegram/releases) the appropriate binary for your OS, place `transmission-telegram` in your `$PATH` and you are good to go.

Or if you have `Go` installed: `go get -u github.com/pyed/transmission-telegram`

## Usage

[Wiki](https://github.com/pyed/transmission-telegram/wiki)

## Docker

Set all the important variables in the file ``docker-compose.yml`` and run command:
```bash
docker-compose up -d --build --force-recreate
```
`HTTPS_PROXY` - variable is set only if access to the ``api.telegram.org`` is closed directly.