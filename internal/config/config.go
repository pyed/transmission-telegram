package config

import (
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/dustin/go-humanize"
)

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

// Set adds a master, or several separated by commas or spaces, as
// TT_MASTERS and config files give them.
func (masters *MasterSlice) Set(value string) error {
	fields := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
	if len(fields) == 0 {
		return errors.New("master cannot be empty")
	}
	for _, master := range fields {
		if strings.Trim(master, "@") == "" {
			return errors.New("master cannot be empty")
		}
		*masters = append(*masters, strings.ToLower(master))
	}
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

// SetupMaster is the master to start the bot with before knowing one's
// user ID: while it is the only one, the bot tells people their ID.
const SetupMaster = "1"

// Setup reports whether the only master is the placeholder SetupMaster.
func (masters MasterSlice) Setup() bool {
	return len(masters) == 1 && masters[0] == SetupMaster
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
	// StateFile is where the bot keeps what it remembers; the default
	// location when empty.
	StateFile string
	// WatchInterval is how often the notification watcher checks
	// Transmission.
	WatchInterval time.Duration
	// LowDisk is the free space, in bytes, below which chats that want it
	// are warned; 0 turns the warnings off.
	LowDisk uint64

	// ConfigFile is the file given with -config, which settings not given
	// as flags were read from.
	ConfigFile string
	// Warnings are problems with the settings worth logging, such as a
	// config file other users may read.
	Warnings []string

	// ShowVersion asks only for the version to be printed; nothing else
	// is validated.
	ShowVersion bool
}

func Parse() *Config {
	flag.Usage = func() {
		fmt.Fprint(os.Stderr, "Usage: transmission-telegram <-token=TOKEN> <-master=123456789> [-master=987654321] [-url=http://] [-username=user] [-password=pass]\n"+
			"       transmission-telegram -config=FILE\n\n")
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
	fs.Var(&cfg.Masters, "master", "Telegram user ID or username allowed to use the bot; repeatable, or separated by commas (env: TT_MASTERS)")
	fs.StringVar(&cfg.RPCURL, "url", defaultRPCURL, "Transmission RPC URL (env: TR_URL)")
	fs.StringVar(&cfg.Username, "username", "", "Transmission username (env: TR_AUTH user:password)")
	fs.StringVar(&cfg.Password, "password", "", "Transmission password (env: TR_AUTH user:password)")
	fs.StringVar(&cfg.LogFile, "logfile", "", "Send logs to a private file, moved aside to FILE.1 at 10 MiB")
	fs.StringVar(&cfg.TransLogFile, "transmission-logfile", "", "Deprecated and ignored: completion notifications come from Transmission's RPC")
	fs.BoolVar(&cfg.NoLive, "no-live", false, "Don't edit and update info after sending")
	fs.StringVar(&cfg.StateFile, "state", "", "File where the bot keeps settings such as where notifications go (env: TT_STATE; default: transmission-telegram/state.json in the user's config directory)")
	fs.DurationVar(&cfg.WatchInterval, "watch-interval", 10*time.Second, "How often to check Transmission for notifications")
	lowDisk := fs.String("low-disk", "5GB", "Warn the chats that want it when free space falls below this size; 0 turns it off")
	fs.StringVar(&cfg.ConfigFile, "config", "", "Read settings from this file: a flag name and its value per line, such as token = 123:abc; "+
		"flags win over the file, and the file over the environment")
	fs.BoolVar(&cfg.ShowVersion, "version", false, "Print the version and exit")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if cfg.ShowVersion {
		return cfg, nil
	}
	if cfg.ConfigFile != "" {
		warning, err := applyConfigFile(fs, cfg.ConfigFile)
		if err != nil {
			return nil, err
		}
		if warning != "" {
			cfg.Warnings = append(cfg.Warnings, warning)
		}
	}

	given := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	if !given["token"] {
		cfg.BotToken = getenv("TT_BOT_TOKEN")
		if strings.TrimSpace(cfg.BotToken) == "" {
			cfg.BotToken = getenv("TT_BOTT")
		}
	}
	if rpcURL := strings.TrimSpace(getenv("TR_URL")); !given["url"] && rpcURL != "" {
		cfg.RPCURL = rpcURL
	}
	if stateFile := strings.TrimSpace(getenv("TT_STATE")); !given["state"] && stateFile != "" {
		cfg.StateFile = stateFile
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
		return nil, errors.New("telegram bot token is required (-token, a config file, or TT_BOT_TOKEN)")
	}
	if masters := getenv("TT_MASTERS"); !given["master"] && strings.TrimSpace(masters) != "" {
		if err := cfg.Masters.Set(masters); err != nil {
			return nil, err
		}
	}
	if len(cfg.Masters) < 1 {
		return nil, errors.New("at least one authorized user is required (-master, a config file, or TT_MASTERS); start with -master=1 to have the bot tell you your user ID")
	}
	if cfg.WatchInterval < time.Second {
		return nil, errors.New("watch-interval must be at least 1s")
	}
	var err error
	if cfg.LowDisk, err = humanize.ParseBytes(*lowDisk); err != nil {
		return nil, errors.New("low-disk must be a size such as 5GB, or 0")
	}
	// The message leaves the URL out: it may carry credentials.
	if u, err := url.Parse(cfg.RPCURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("transmission RPC URL (-url or TR_URL) must be an http:// or https:// URL, such as " + defaultRPCURL)
	}

	for i := range cfg.Masters {
		cfg.Masters[i] = strings.TrimPrefix(strings.TrimSpace(cfg.Masters[i]), "@")
	}

	return cfg, nil
}
