package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/pyed/transmission"
)

var (
	token    string
	master   string
	url      string
	username string
	password string

	client *transmission.Client
)

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

	// set transmission.Config, needed to establish a connection with transmission
	conf := transmission.Config{
		Address:  url,
		User:     username,
		Password: password,
	}

	// transmission.New() never returns an error, we will ignore it and test with client.Session.Update()
	client, _ = transmission.New(conf)
	if err := client.Session.Update(); err != nil {

		// try to predict the error message, as it vague coming from pyed/transmission
		if strings.HasPrefix(err.Error(), "invalid character") { // means the user or the pass is wrong.
			fmt.Fprintln(os.Stderr, "Transmission's Username or Password is wrong.\n")

		} else { // any other error is probaby because of the URL
			fmt.Fprintf(os.Stderr, "Error: Couldn't connect to: %s\n", url)
			fmt.Fprintln(os.Stderr, "Make sure to pass the right full RPC URL e.g. http://localhost:9091/transmission/rpc\n")
		}
		// send the vague error message too
		fmt.Fprintln(os.Stderr, "RawError: "+err.Error())
		os.Exit(1)
	}
}

func main() {}
