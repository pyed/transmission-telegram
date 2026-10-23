package config

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
)

const Help = `
*Torrent lists*
/list (optional tracker) — all torrents, optionally filtered by tracker (/li, /ls)
/head (optional n) — first n torrents with live speeds (/he)
/tail (optional n) — last n torrents with live speeds (/ta)
/active — actively transferring torrents (/ac)
/downs — downloading and queued torrents (/dg)
/seeding — seeding and queued torrents (/sd)
/paused — stopped torrents (/pa)
/checking — verifying and queued torrents (/ch)
/errors — torrents with errors (/er)
/latest (optional n) — newest torrents; default 5 (/la)
/search <name> — search torrent names (/se)
/info <id...> — details and inline controls (/in)

*Torrent control*
/add <url|magnet...> — add one or more torrents (/ad)
You can also send a .torrent document directly.
/stop <id...|all> — pause torrents (/sp)
/start <id...|all> — resume torrents (/st)
/check <id...|all> — verify torrents (/ck)
/del <id...> — remove torrents, keeping local data (/rm)
/deldata <id...> — permanently remove torrents and local data
/reannounce <id...|all> — contact trackers now (/ra)

*Session and settings*
/speed — current transfer speeds (/ss)
/stats — session and cumulative statistics (/sa)
/count — torrent counts by status (/co)
/trackers — tracker hosts and torrent counts (/tr)
/sort (optional rev) <method> — set listing order (/so)
/turtle (optional on|off) — alternative speed limits (/alt, /tu)
/free (optional path) — available disk space (/space, /disk)
/downlimit <KB/s> — global download limit (/dl)
/uplimit <KB/s> — global upload limit (/ul)
/downloaddir <path> — set the default download directory (/dd)
/version — Transmission and bot versions (/ver)
/help — show this message

Only authorized users are accepted. In groups, Telegram may display commands as /command@BotName.
Be careful with /deldata: it deletes downloaded files immediately.
Report issues: https://github.com/pyed/transmission-telegram`

// Version is overridden by release builds. Tagged go install builds fall back
// to the module version recorded in Go build information.
var Version = "dev"

func init() {
	if Version != "dev" {
		return
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		Version = info.Main.Version
	}
}

type MasterSlice []string

func (masters *MasterSlice) String() string {
	return fmt.Sprintf("%s", *masters)
}

func (masters *MasterSlice) Set(master string) error {
	master = strings.TrimSpace(master)
	if strings.Trim(master, "@") == "" {
		return errors.New("master cannot be empty")
	}
	*masters = append(*masters, strings.ToLower(master))
	return nil
}

func (masters MasterSlice) Contains(username string, userID int64) bool {
	cleanUsername := strings.ToLower(strings.TrimPrefix(username, "@"))
	userIDStr := strconv.FormatInt(userID, 10)

	for _, master := range masters {
		cleanMaster := strings.ToLower(strings.TrimPrefix(master, "@"))
		if cleanUsername != "" && cleanMaster == cleanUsername {
			return true
		}
		if userID > 0 && cleanMaster == userIDStr {
			return true
		}
	}
	return false
}

// Usernames returns the masters given by username rather than numeric user
// ID. Telegram usernames can be changed and then claimed by someone else,
// who would then control the bot.
func (masters MasterSlice) Usernames() []string {
	var usernames []string
	for _, master := range masters {
		if id, err := strconv.ParseInt(master, 10, 64); err != nil || id <= 0 {
			usernames = append(usernames, master)
		}
	}
	return usernames
}

// defaultRPCURL is where Transmission answers RPC out of the box.
const defaultRPCURL = "http://localhost:9091/transmission/rpc"

type Config struct {
	BotToken     string
	Masters      MasterSlice
	RPCURL       string
	Username     string
	Password     string
	LogFile      string
	TransLogFile string
	NoLive       bool
	Interval     time.Duration
	Duration     int

	// ShowVersion asks only for the version to be printed; nothing else
	// is validated.
	ShowVersion bool
}

func Parse() *Config {
	flag.Usage = func() {
		fmt.Fprint(os.Stderr, "Usage: transmission-telegram <-token=TOKEN> <-master=@tuser|123456789> [-master=@yuser2] [-url=http://] [-username=user] [-password=pass]\n\n")
		flag.PrintDefaults()
	}

	cfg, err := ParseFlags(flag.CommandLine, os.Args[1:], os.Getenv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n\n", err)
		flag.Usage()
		os.Exit(1)
	}

	return cfg
}

func ParseFlags(fs *flag.FlagSet, args []string, getenv func(string) string) (*Config, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	cfg := &Config{
		Interval: 5 * time.Second,
		Duration: 10,
	}

	// Values from the environment are applied after parsing rather than as
	// flag defaults: flag defaults are printed by -h and after every usage
	// error, which would put the token and passwords in logs.
	fs.StringVar(&cfg.BotToken, "token", "", "Telegram bot token (env: TT_BOT_TOKEN or legacy TT_BOTT)")
	fs.Var(&cfg.Masters, "master", "Telegram username or numeric user ID allowed to use the bot; repeatable")
	fs.StringVar(&cfg.RPCURL, "url", defaultRPCURL, "Transmission RPC URL (env: TR_URL)")
	fs.StringVar(&cfg.Username, "username", "", "Transmission username (env: TR_AUTH user:password)")
	fs.StringVar(&cfg.Password, "password", "", "Transmission password (env: TR_AUTH user:password)")
	fs.StringVar(&cfg.LogFile, "logfile", "", "Send logs to a private file, moved aside to FILE.1 at 10 MiB")
	fs.StringVar(&cfg.TransLogFile, "transmission-logfile", "", "Open transmission logfile to monitor torrents completion")
	fs.BoolVar(&cfg.NoLive, "no-live", false, "Don't edit and update info after sending")
	fs.BoolVar(&cfg.ShowVersion, "version", false, "Print the version and exit")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if cfg.ShowVersion {
		return cfg, nil
	}

	given := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	if !given["token"] {
		cfg.BotToken = getenv("TT_BOT_TOKEN")
		if strings.TrimSpace(cfg.BotToken) == "" {
			cfg.BotToken = getenv("TT_BOTT")
		}
	}
	if url := strings.TrimSpace(getenv("TR_URL")); !given["url"] && url != "" {
		cfg.RPCURL = url
	}
	if username, password, ok := strings.Cut(getenv("TR_AUTH"), ":"); ok {
		if !given["username"] {
			cfg.Username = username
		}
		if !given["password"] {
			cfg.Password = password
		}
	}

	cfg.BotToken = strings.TrimSpace(cfg.BotToken)
	cfg.RPCURL = strings.TrimSpace(cfg.RPCURL)
	if cfg.BotToken == "" {
		return nil, errors.New("telegram bot token is required (-token or TT_BOT_TOKEN)")
	}
	if len(cfg.Masters) < 1 {
		return nil, errors.New("at least one authorized user is required (-master)")
	}

	for i := range cfg.Masters {
		cfg.Masters[i] = strings.TrimPrefix(strings.TrimSpace(cfg.Masters[i]), "@")
	}

	return cfg, nil
}
