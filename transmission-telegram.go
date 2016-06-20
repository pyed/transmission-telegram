package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"gopkg.in/telegram-bot-api.v4"

	"github.com/pyed/transmission"
)

const VERSION = "0.0"

var (
	// flags
	BotToken string
	Master   string
	RpcUrl   string
	Username string
	Password string

	// transmission
	Client *transmission.Client

	// telegram
	Bot     *tgbotapi.BotAPI
	Updates <-chan tgbotapi.Update
)

// init flags
func init() {
	// define arguments and parse them.
	flag.StringVar(&BotToken, "token", "", "Telegram bot token")
	flag.StringVar(&Master, "master", "", "Your telegram handler, So the bot will only respond to you")
	flag.StringVar(&RpcUrl, "url", "http://localhost:9091/transmission/rpc", "Transmission RPC URL")
	flag.StringVar(&Username, "username", "", "Transmission username")
	flag.StringVar(&Password, "password", "", "Transmission password")

	// set the usage message
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: transmission-bot -token=<TOKEN> -master=<@tuser> -url=[http://] -username=[user] -password=[pass]\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	// make sure that we have the two madatory arguments: telegram token & master's handler.
	if BotToken == "" ||
		Master == "" {
		fmt.Fprintf(os.Stderr, "Error: Mandatory argument missing!\n\n")
		flag.Usage()
		os.Exit(1)
	}

	// make sure that the handler doesn't contain @
	if strings.Contains(Master, "@") {
		Master = strings.Replace(Master, "@", "", -1)
	}
}

// init transmission
func init() {
	// set transmission.Config, needed to establish a connection with transmission
	conf := transmission.Config{
		Address:  RpcUrl,
		User:     Username,
		Password: Password,
	}

	// transmission.New() never returns an error, we will ignore it and test with client.Session.Update()
	Client, _ = transmission.New(conf)
	if err := Client.Session.Update(); err != nil {

		// try to predict the error message, as it vague coming from pyed/transmission
		if strings.HasPrefix(err.Error(), "invalid character") { // means the user or the pass is wrong.
			fmt.Fprintf(os.Stderr, "Transmission's Username or Password is wrong.\n\n")

		} else { // any other error is probaby because of the URL
			fmt.Fprintf(os.Stderr, "Error: Couldn't connect to: %s\n", RpcUrl)
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
	Bot, err = tgbotapi.NewBotAPI(BotToken)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Telegram Error: %s\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stdout, "Authorized on %s", Bot.Self.UserName)

	// get a channel and sign it to 'Updates'
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	Updates, err = Bot.GetUpdatesChan(u)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Telegram Error: %s\n", err)
		os.Exit(1)
	}
}

func main() {
	for update := range Updates {
		// ignore anyone other than 'master'
		if strings.ToLower(update.Message.From.UserName) != strings.ToLower(Master) {
			continue
		}
		// ignore edited messages
		if update.Message == nil {
			continue
		}

		// tokenize the update
		tokens := strings.Split(update.Message.Text, " ")
		command := strings.ToLower(tokens[0])

		switch command {
		case "list", "/list":
			// list torrents
			go list(&update)

		case "downs", "/downs":
			// list downloading
		case "active", "/active":
			// list active torrents
		case "errors", "/errors":
			// list torrents with errors
		case "trackers", "/trackers":
			// list trackers
		case "add", "/add":
			// takes url to a torrent to add it
		case "search", "/search":
			// search for a torrent
		case "latest", "/latest":
			// get the latest torrents
		case "info", "/info":
			// gets info on specific torrent
		case "stop", "/stop":
			// stop one torrent or more
		case "stopall", "/stopall":
			// stops all the torrents
		case "start", "/start":
			// starts one torrent or more
		case "startall", "/startall":
			// starts all the torrents
		case "stats", "/stats":
			// print transmission stats
		case "speed", "/speed":
			// print current download and upload speeds
		case "del", "/del":
			// deletes a torrent but keep its data
		case "deldata", "/deldata":
			// deletes a torrents and its data
		case "help", "/help":
			// prints a help message
		case "version", "/version":
			// print transmission and transmission-telegram versions
		default:
			// no such command, try help

		}
	}
}

// list will form and send a list of all the torrents
func list(ud *tgbotapi.Update) {
	torrents, err := Client.GetTorrents()
	if err != nil {
		send("list: "+err.Error(), ud.Message.Chat.ID)
		return
	}

	buf := new(bytes.Buffer)
	for i := range torrents {
		buf.WriteString(fmt.Sprintf("<%d> %s\n", torrents[i].ID, torrents[i].Name))
	}

	if buf.Len() == 0 {
		send("No torrents exist!", ud.Message.Chat.ID)
		return
	}

	send(buf.String(), ud.Message.Chat.ID)
}

// send takes a chat id and a message to send.
func send(text string, chatID int64) {
	// set typing action
	action := tgbotapi.NewChatAction(chatID, tgbotapi.ChatTyping)
	Bot.Send(action)

	// check the rune count, telegram is limited to 4096 chars per message;
	// so if our message is > 4096, split it in chunks the send them.
	msgRuneCount := utf8.RuneCountInString(text)
LenCheck:
	if msgRuneCount > 4096 {
		msg := tgbotapi.NewMessage(chatID, text[:4095])

		// send current chunk
		if _, err := Bot.Send(msg); err != nil {
			fmt.Fprintf(os.Stderr, "send error: %s\n", err)
		}
		// move to the next chunk
		text = text[4095:]
		msgRuneCount = utf8.RuneCountInString(text)
		goto LenCheck
	}

	// if msgRuneCount < 4096, send it normally
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := Bot.Send(msg); err != nil {
		fmt.Fprint(os.Stderr, "send error: %s\n", err)
	}
}
