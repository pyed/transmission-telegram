package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"gopkg.in/telegram-bot-api.v4"

	"github.com/pyed/transmission"
)

var (
	// flags
	token    string
	master   string
	url      string
	username string
	password string

	// transmission
	Client *transmission.Client

	// telegram
	Bot     *tgbotapi.BotAPI
	Updates <-chan tgbotapi.Update
)

// init flags
func init() {
	// define arguments and parse them.
	flag.StringVar(&token, "token", "", "Telegram bot token")
	flag.StringVar(&master, "master", "", "Your telegram handler, So the bot will only respond to you")
	flag.StringVar(&url, "url", "http://localhost:9091/transmission/rpc", "Transmission RPC URL")
	flag.StringVar(&username, "username", "", "Transmission username")
	flag.StringVar(&password, "password", "", "Transmission password")

	// set the usage message
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: transmission-bot -token=<TOKEN> -master=<@tuser> -url=[http://] -username=[user] -password=[pass]\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	// make sure that we have the two madatory arguments: telegram token & master's handler.
	if token == "" ||
		master == "" {
		fmt.Fprintf(os.Stderr, "Error: Mandatory argument missing!\n\n")
		flag.Usage()
		os.Exit(1)
	}
}

// init transmission
func init() {
	// set transmission.Config, needed to establish a connection with transmission
	conf := transmission.Config{
		Address:  url,
		User:     username,
		Password: password,
	}

	// transmission.New() never returns an error, we will ignore it and test with client.Session.Update()
	Client, _ = transmission.New(conf)
	if err := Client.Session.Update(); err != nil {

		// try to predict the error message, as it vague coming from pyed/transmission
		if strings.HasPrefix(err.Error(), "invalid character") { // means the user or the pass is wrong.
			fmt.Fprintf(os.Stderr, "Transmission's Username or Password is wrong.\n\n")

		} else { // any other error is probaby because of the URL
			fmt.Fprintf(os.Stderr, "Error: Couldn't connect to: %s\n", url)
			fmt.Fprintf(os.Stderr, "Make sure to pass the right full RPC URL e.g. http://localhost:9091/transmission/rpc\n\n")
		}
		// send the vague error message too
		fmt.Fprintf(os.Stderr, "JSONError: %s\n", err)
		os.Exit(1)
	}
}

// init telegram
func init() {
	// authorize using the token
	var err error
	Bot, err = tgbotapi.NewBotAPI(token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Telegram Error: %s\n", err)
		os.Exit(1)
	}

	// get a channel and sign it to 'Updates'
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	Updates, err = Bot.GetUpdatesChan(u)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Telegram Error: %s\n", err)
		os.Exit(1)
	}
}

func main() {}
