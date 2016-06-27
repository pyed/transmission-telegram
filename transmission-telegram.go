package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/telegram-bot-api.v4"

	"github.com/dustin/go-humanize"
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
	Client transmission.TransmissionClient

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
	// conf := transmission.Config{
	// 	Address:  RpcUrl,
	// 	User:     Username,
	// 	Password: Password,
	// }

	// transmission.New() never returns an error, we will ignore it and test with client.Session.Update()
	Client = transmission.New(RpcUrl, Username, Password)
	// if err := Client.Session.Update(); err != nil {

	// try to predict the error message, as it vague coming from pyed/transmission
	// 	if strings.HasPrefix(err.Error(), "invalid character") { // means the user or the pass is wrong.
	// 		fmt.Fprintf(os.Stderr, "Transmission's Username or Password is wrong.\n\n")

	// 	} else { // any other error is probaby because of the URL
	// 		fmt.Fprintf(os.Stderr, "Error: Couldn't connect to: %s\n", RpcUrl)
	// 		fmt.Fprintf(os.Stderr, "Make sure to pass the right full RPC URL e.g. http://localhost:9091/transmission/rpc\n\n")
	// 	}
	// 	// send the vague error message too
	// 	fmt.Fprintf(os.Stderr, "JSONError: %s\n", err)
	// 	os.Exit(1)
	// }
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
			// TODO take argument as tracker and list those only
			go list(&update)

		case "downs", "/downs":
			// list downloading
			go downs(&update)

		case "active", "/active":
			// list active torrents
			go active(&update)

		case "errors", "/errors":
			// list torrents with errors
			go errors(&update)

		case "trackers", "/trackers":
			// list trackers
			go trackers(&update)

		case "add", "/add":
			// takes url to a torrent to add it
			go add(&update, tokens[1:])

		case "search", "/search":
			// search for a torrent
			go search(&update, tokens[1:])

		case "latest", "/latest":
			// get the latest torrents
			go latest(&update, tokens[1:])

		case "info", "/info":
			// gets info on specific torrent
			go info(&update, tokens[1:])

		case "stop", "/stop":
			// stop one torrent or more
			go stop(&update, tokens[1:])

		case "start", "/start":
			// starts one torrent or more
			go start(&update, tokens[1:])

		case "check", "/check":
			// verify a torrent or torrents
			go check(&update, tokens[1:])

		case "stats", "/stats":
			// print transmission stats
			go stats(&update)

		case "speed", "/speed":
			// print current download and upload speeds
			go speed(&update)

		case "del", "/del":
			// deletes a torrent but keep its data
			go del(&update, tokens[1:])

		case "deldata", "/deldata":
			// deletes a torrents and its data
			go deldata(&update, tokens[1:])

		case "help", "/help":
			// prints a help message
		case "version", "/version":
			// print transmission and transmission-telegram versions
			go version(&update)

		case "":
			// might be a file received
			go receiveTorrent(&update)

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

// downs will send the names of torrents with status: Downloading
func downs(ud *tgbotapi.Update) {
	torrents, err := Client.GetTorrents()
	if err != nil {
		send("downs: "+err.Error(), ud.Message.Chat.ID)
		return
	}

	buf := new(bytes.Buffer)
	for i := range torrents {
		// Downloading or in queue to download
		if torrents[i].Status == 4 ||
			torrents[i].Status == 3 {
			buf.WriteString(fmt.Sprintf("<%d> %s\n", torrents[i].ID, torrents[i].Name))
		}
	}

	if buf.Len() == 0 {
		send("No downloads", ud.Message.Chat.ID)
		return
	}
	send(buf.String(), ud.Message.Chat.ID)
}

// active will send torrents that are actively uploading
func active(ud *tgbotapi.Update) {
	torrents, err := Client.GetTorrents()
	if err != nil {
		send("active: "+err.Error(), ud.Message.Chat.ID)
		return
	}

	buf := new(bytes.Buffer)
	for i := range torrents {
		if torrents[i].RateUpload > 0 {
			buf.WriteString(fmt.Sprintf("<%d> %s\n\t⬆ %s\n",
				torrents[i].ID, torrents[i].Name, humanize.Bytes(torrents[i].RateUpload)))
		}
	}
	if buf.Len() == 0 {
		send("No active torrents", ud.Message.Chat.ID)
		return
	}
	send(buf.String(), ud.Message.Chat.ID)
}

// errors will send torrents with errors
func errors(ud *tgbotapi.Update) {
	torrents, err := Client.GetTorrents()
	if err != nil {
		send("errors: "+err.Error(), ud.Message.Chat.ID)
		return
	}

	buf := new(bytes.Buffer)
	for i := range torrents {
		if torrents[i].Error != 0 {
			buf.WriteString(fmt.Sprintf("<%d> %s\n%s\n",
				torrents[i].ID, torrents[i].Name, torrents[i].ErrorString))
		}
	}
	if buf.Len() == 0 {
		send("No errors", ud.Message.Chat.ID)
		return
	}
	send(buf.String(), ud.Message.Chat.ID)
}

var trackerRegex = regexp.MustCompile(`https?://([^:/]*)`)

// trackers will send a list of trackers and how many torrents each one has
func trackers(ud *tgbotapi.Update) {
	torrents, err := Client.GetTorrents()
	if err != nil {
		send("trackers: "+err.Error(), ud.Message.Chat.ID)
		return
	}

	trackers := make(map[string]int)

	for i := range torrents {
		for _, tracker := range torrents[i].Trackers {
			sm := trackerRegex.FindSubmatch([]byte(tracker.Announce))
			if len(sm) > 1 {
				currentTracker := string(sm[1])
				n, ok := trackers[currentTracker]
				if !ok {
					trackers[currentTracker] = 1
					continue
				}
				trackers[currentTracker] = n + 1
			}
		}
	}

	buf := new(bytes.Buffer)
	for k, v := range trackers {
		buf.WriteString(fmt.Sprintf("%d - %s\n", v, k))
	}

	if buf.Len() == 0 {
		send("No trackers!", ud.Message.Chat.ID)
		return
	}
	send(buf.String(), ud.Message.Chat.ID)
}

// add takes an URL to a .torrent file to add it to transmission
func add(ud *tgbotapi.Update, tokens []string) {
	if len(tokens) == 0 {
		send("add: needs atleast one URL", ud.Message.Chat.ID)
		return
	}

	// loop over the URL/s and add them
	for _, url := range tokens {
		cmd := transmission.NewAddCmdByURL(url)

		torrent, err := Client.ExecuteAddCommand(cmd)
		if err != nil {
			send("add: "+err.Error(), ud.Message.Chat.ID)
			continue
		}

		// check if torrent.Name is empty, then an error happened
		if torrent.Name == "" {
			send("add: error adding "+url, ud.Message.Chat.ID)
			continue
		}
		send("Added: "+torrent.Name, ud.Message.Chat.ID)
	}
}

// receiveTorrent gets an update that potentially has a .torrent file to add
func receiveTorrent(ud *tgbotapi.Update) {
	if ud.Message.Document.FileID == "" {
		return // has no document
	}

	// get the file ID and make the config
	fconfig := tgbotapi.FileConfig{ud.Message.Document.FileID}
	file, err := Bot.GetFile(fconfig)
	if err != nil {
		send("receiver: "+err.Error(), ud.Message.Chat.ID)
		return
	}

	// add by file URL
	add(ud, []string{file.Link(BotToken)})
}

// search takes a query and returns torrents with match
func search(ud *tgbotapi.Update, tokens []string) {
	// make sure that we got a query
	if len(tokens) == 0 {
		send("search: needs an argument", ud.Message.Chat.ID)
		return
	}

	query := strings.Join(tokens, " ")
	// "(?i)" for case insensitivity
	regx, err := regexp.Compile("(?i)" + query)
	if err != nil {
		send("search: "+err.Error(), ud.Message.Chat.ID)
		return
	}

	torrents, err := Client.GetTorrents()
	if err != nil {
		send("search: "+err.Error(), ud.Message.Chat.ID)
		return
	}

	buf := new(bytes.Buffer)
	for i := range torrents {
		if regx.MatchString(torrents[i].Name) {
			buf.WriteString(fmt.Sprintf("<%d> %s\n", torrents[i].ID, torrents[i].Name))
		}
	}
	if buf.Len() == 0 {
		send("No matches!", ud.Message.Chat.ID)
		return
	}
	send(buf.String(), ud.Message.Chat.ID)
}

// latest takes n and returns the latest n torrents
func latest(ud *tgbotapi.Update, tokens []string) {
	var (
		n   = 5 // default to 5
		err error
	)

	if len(tokens) > 0 {
		n, err = strconv.Atoi(tokens[0])
		if err != nil {
			send("latest: argument must be a number", ud.Message.Chat.ID)
			return
		}
	}

	torrents, err := Client.GetTorrents()
	if err != nil {
		send("latest: "+err.Error(), ud.Message.Chat.ID)
		return
	}

	// make sure that we stay in the boundaries
	torrentsLen := len(torrents)
	if n <= 0 || n > torrentsLen {
		n = torrentsLen
	}

	// sort by addedDate, and set reverse to true to get the latest first
	torrents.SortByAddedDate(true)

	buf := new(bytes.Buffer)
	for i := range torrents[:n] {
		buf.WriteString(fmt.Sprintf("<%d> %s\n", torrents[i].ID, torrents[i].Name))
	}
	if buf.Len() == 0 {
		send("No torrents", ud.Message.Chat.ID)
		return
	}
	send(buf.String(), ud.Message.Chat.ID)
}

// info takes an id of a torrent and returns some info about it
func info(ud *tgbotapi.Update, tokens []string) {
	if len(tokens) == 0 {
		send("info: needs a torrent ID number", ud.Message.Chat.ID)
		return
	}

	// try to read the id
	num, err := strconv.Atoi(tokens[0])
	if err != nil {
		send(fmt.Sprintf("info: %s is not a number", tokens[0]), ud.Message.Chat.ID)
		return
	}

	// get the torrent
	torrent, err := Client.GetTorrent(num)
	if err != nil {
		send(fmt.Sprintf("info: Can't find a torrent with an ID of %d", num), ud.Message.Chat.ID)
		return
	}

	// format the info
	info := fmt.Sprintf("<%d> %s\n%s\t%s of %s (%.2f%%)\t↓ %s  ↑ %s R:%.3f\nUP: %s  DL: %s  Added: %s  ETA: %d\nTracker: %s",
		torrent.ID, torrent.Name, torrent.TorrentStatus(), humanize.Bytes(torrent.DownloadedEver), humanize.Bytes(torrent.SizeWhenDone),
		torrent.PercentDone*100, humanize.Bytes(torrent.RateDownload), humanize.Bytes(torrent.RateUpload), torrent.UploadRatio,
		humanize.Bytes(torrent.UploadedEver), humanize.Bytes(torrent.DownloadedEver), time.Unix(torrent.AddedDate, 0).Format(time.Stamp),
		torrent.Eta, torrent.Trackers[0].Announce)
	// trackers should be fixed

	// send it
	send(info, ud.Message.Chat.ID)
}

// stop takes id[s] of torrent[s] or 'all' to stop them
func stop(ud *tgbotapi.Update, tokens []string) {
	// make sure that we got at least one argument
	if len(tokens) == 0 {
		send("stop: needs an argument", ud.Message.Chat.ID)
		return
	}

	// if the first argument is 'all' then stop all torrents
	if tokens[0] == "all" {
		if err := Client.StopAll(); err != nil {
			send("stop: error occurred while stopping some torrents", ud.Message.Chat.ID)
			return
		}
		send("stopped all torrents", ud.Message.Chat.ID)
		return
	}

	for _, id := range tokens {
		num, err := strconv.Atoi(id)
		if err != nil {
			send(fmt.Sprintf("stop: %s is not a number", id), ud.Message.Chat.ID)
			continue
		}
		status, err := Client.StopTorrent(num)
		if err != nil {
			send("stop: "+err.Error(), ud.Message.Chat.ID)
			continue
		}

		torrent, err := Client.GetTorrent(num)
		if err != nil {
			send(fmt.Sprintf("[fail] stop: No torrent with an ID of %d", num), ud.Message.Chat.ID)
			return
		}
		send(fmt.Sprintf("[%s] stop: %s", status, torrent.Name), ud.Message.Chat.ID)
	}
}

// start takes id[s] of torrent[s] or 'all' to start them
func start(ud *tgbotapi.Update, tokens []string) {
	// make sure that we got at least one argument
	if len(tokens) == 0 {
		send("start: needs an argument", ud.Message.Chat.ID)
		return
	}

	// if the first argument is 'all' then start all torrents
	if tokens[0] == "all" {
		if err := Client.StartAll(); err != nil {
			send("start: error occurred while starting some torrents", ud.Message.Chat.ID)
			return
		}
		send("started all torrents", ud.Message.Chat.ID)
		return

	}

	for _, id := range tokens {
		num, err := strconv.Atoi(id)
		if err != nil {
			send(fmt.Sprintf("start: %s is not a number", id), ud.Message.Chat.ID)
			continue
		}
		status, err := Client.StartTorrent(num)
		if err != nil {
			send("stop: "+err.Error(), ud.Message.Chat.ID)
			continue
		}

		torrent, err := Client.GetTorrent(num)
		if err != nil {
			send(fmt.Sprintf("[fail] start: No torrent with an ID of %d", num), ud.Message.Chat.ID)
			return
		}
		send(fmt.Sprintf("[%s] start: %s", status, torrent.Name), ud.Message.Chat.ID)
	}
}

// check takes id[s] of torrent[s] or 'all' to verify them
func check(ud *tgbotapi.Update, tokens []string) {
	// make sure that we got at least one argument
	if len(tokens) == 0 {
		send("check: needs an argument", ud.Message.Chat.ID)
		return
	}

	// if the first argument is 'all' then start all torrents
	if tokens[0] == "all" {
		if err := Client.VerifyAll(); err != nil {
			send("check: error occurred while verifying some torrents", ud.Message.Chat.ID)
			return
		}
		send("verifying all torrents", ud.Message.Chat.ID)
		return

	}

	for _, id := range tokens {
		num, err := strconv.Atoi(id)
		if err != nil {
			send(fmt.Sprintf("check: %s is not a number", id), ud.Message.Chat.ID)
			continue
		}
		status, err := Client.VerifyTorrent(num)
		if err != nil {
			send("stop: "+err.Error(), ud.Message.Chat.ID)
			continue
		}

		torrent, err := Client.GetTorrent(num)
		if err != nil {
			send(fmt.Sprintf("[fail] check: No torrent with an ID of %d", num), ud.Message.Chat.ID)
			return
		}
		send(fmt.Sprintf("[%s] check: %s", status, torrent.Name), ud.Message.Chat.ID)
	}

}

// stats echo back transmission stats
func stats(ud *tgbotapi.Update) {
	stats, err := Client.GetStats()
	if err != nil {
		send("stats: "+err.Error(), ud.Message.Chat.ID)
		return
	}

	msg := fmt.Sprintf(
		`
		Total torrents: %d
		Active: %d
		Paused: %d

		- Current Stats -
		Downloaded: %s
		Uploaded: %s
		Running time: %d seconds

		- Total Stats -
		Sessions: %d
		Downloaded: %s
		Uploaded: %s
		Total Running time: %d seconds
		`,

		stats.TorrentCount,
		stats.ActiveTorrentCount,
		stats.PausedTorrentCount,
		humanize.Bytes(stats.CurrentStats.DownloadedBytes),
		humanize.Bytes(stats.CurrentStats.UploadedBytes),
		stats.CurrentStats.SecondsActive,
		stats.CumulativeStats.SessionCount,
		humanize.Bytes(stats.CumulativeStats.DownloadedBytes),
		humanize.Bytes(stats.CumulativeStats.UploadedBytes),
		stats.CumulativeStats.SecondsActive,
	)

	send(msg, ud.Message.Chat.ID)
}

// speed will echo back the current download and upload speeds
func speed(ud *tgbotapi.Update) {
	stats, err := Client.GetStats()
	if err != nil {
		send("speed: "+err.Error(), ud.Message.Chat.ID)
		return
	}

	msg := fmt.Sprintf("⬇ %s  ⬆ %s", humanize.Bytes(stats.DownloadSpeed), humanize.Bytes(stats.UploadSpeed))
	send(msg, ud.Message.Chat.ID)
}

// del takes an id or more, and delete the corresponding torrent/s
func del(ud *tgbotapi.Update, tokens []string) {
	// make sure that we got an argument
	if len(tokens) == 0 {
		send("del: needs an ID", ud.Message.Chat.ID)
		return
	}

	// loop over tokens to read each potential id
	for _, id := range tokens {
		num, err := strconv.Atoi(id)
		if err != nil {
			send(fmt.Sprintf("del: %s is not an ID", id), ud.Message.Chat.ID)
			return
		}

		name, err := Client.DeleteTorrent(num, false)
		if err != nil {
			send("del: "+err.Error(), ud.Message.Chat.ID)
			return
		}

		send("Deleted: "+name, ud.Message.Chat.ID)
	}
}

// deldata takes an id or more, and delete the corresponding torrent/s with their data
func deldata(ud *tgbotapi.Update, tokens []string) {
	// make sure that we got an argument
	if len(tokens) == 0 {
		send("deldata: needs an ID", ud.Message.Chat.ID)
		return
	}
	// loop over tokens to read each potential id
	for _, id := range tokens {
		num, err := strconv.Atoi(id)
		if err != nil {
			send(fmt.Sprintf("deldata: %s is not an ID", id), ud.Message.Chat.ID)
			return
		}

		name, err := Client.DeleteTorrent(num, true)
		if err != nil {
			send("deldata: "+err.Error(), ud.Message.Chat.ID)
			return
		}

		send("Deleted with data: "+name, ud.Message.Chat.ID)
	}
}

// help

// version sends transmission version + transmission-telegram version
func version(ud *tgbotapi.Update) {
	send(fmt.Sprintf("Transmission %s\nTransmission-telegram %s", Client.Version(), VERSION), ud.Message.Chat.ID)
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
		msg.DisableWebPagePreview = true

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
	msg.DisableWebPagePreview = true
	if _, err := Bot.Send(msg); err != nil {
		fmt.Fprint(os.Stderr, "send error: %s\n", err)
	}
}
