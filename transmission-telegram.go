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
	Client *transmission.TransmissionClient

	// telegram
	Bot     *tgbotapi.BotAPI
	Updates <-chan tgbotapi.Update

	// interval in seconds for live updates, affects "active", "info", "speed"
	interval time.Duration = 2
	// duration controls how many intervals will happen
	duration = 10
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
		fmt.Fprint(os.Stderr, "Usage: transmission-bot -token=<TOKEN> -master=<@tuser> -url=[http://] -username=[user] -password=[pass]\n\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	// make sure that we have the two madatory arguments: telegram token & master's handler.
	if BotToken == "" ||
		Master == "" {
		fmt.Fprintf(os.Stderr, "Error: Mandatory argument missing! (-token or -master)\n\n")
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
	var err error
	Client, err = transmission.New(RpcUrl, Username, Password)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: Make sure you have the right URL, Username and Password\n")
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
		// ignore edited messages
		if update.Message == nil {
			continue
		}

		// ignore anyone other than 'master'
		if strings.ToLower(update.Message.From.UserName) != strings.ToLower(Master) {
			continue
		}

		// tokenize the update
		tokens := strings.Split(update.Message.Text, " ")
		command := strings.ToLower(tokens[0])

		switch command {
		case "list", "/list", "li", "/li":
			// list torrents
			go list(update, tokens[1:])

		case "head", "/head", "he", "/he":
			// list the first 5 or n torrents
			go head(update, tokens[1:])

		case "tail", "/tail", "ta", "/ta":
			// list the last 5 or n torrents
			go tail(update, tokens[1:])

		case "downs", "/downs", "do", "/do":
			// list downloading
			go downs(update)

		case "seeding", "/seeding", "sd", "/sd":
			// list seeding
			go seeding(update)

		case "paused", "/paused", "pa", "/pa":
			// list puased torrents
			go paused(update)

		case "checking", "/checking", "ch", "/ch":
			// list verifying torrents
			go checking(update)

		case "active", "/active", "ac", "/ac":
			// list active torrents
			go active(update)

		case "errors", "/errors", "er", "/er":
			// list torrents with errors
			go errors(update)

		case "sort", "/sort", "so", "/so":
			// sort torrents
			go sort(update, tokens[1:])

		case "trackers", "/trackers", "tr", "/tr":
			// list trackers
			go trackers(update)

		case "add", "/add", "ad", "/ad":
			// takes url to a torrent to add it
			go add(update, tokens[1:])

		case "search", "/search", "se", "/se":
			// search for a torrent
			go search(update, tokens[1:])

		case "latest", "/latest", "la", "/la":
			// get the latest torrents
			go latest(update, tokens[1:])

		case "info", "/info", "in", "/in":
			// gets info on specific torrent
			go info(update, tokens[1:])

		case "stop", "/stop", "sp", "/sp":
			// stop one torrent or more
			go stop(update, tokens[1:])

		case "start", "/start", "st", "/st":
			// starts one torrent or more
			go start(update, tokens[1:])

		case "check", "/check", "ck", "/ck":
			// verify a torrent or torrents
			go check(update, tokens[1:])

		case "stats", "/stats", "sa", "/sa":
			// print transmission stats
			go stats(update)

		case "speed", "/speed", "ss", "/ss":
			// print current download and upload speeds
			go speed(update)

		case "count", "/count", "co", "/co":
			// sends current torrents count per status
			go count(update)

		case "del", "/del":
			// deletes a torrent but keep its data
			go del(update, tokens[1:])

		case "deldata", "/deldata":
			// deletes a torrents and its data
			go deldata(update, tokens[1:])

		case "help", "/help":
			// prints a help message
		case "version", "/version":
			// print transmission and transmission-telegram versions
			go version(update)

		case "":
			// might be a file received
			go receiveTorrent(update)

		default:
			// no such command, try help
			go send("no such command, try /help", update.Message.Chat.ID)

		}
	}
}

// list will form and send a list of all the torrents
func list(ud tgbotapi.Update, tokens []string) {
	torrents, err := Client.GetTorrents()
	if err != nil {
		send("list: "+err.Error(), ud.Message.Chat.ID)
		return
	}

	buf := new(bytes.Buffer)
	// if it gets a query, it will list torrents that has trackers that match the query
	if len(tokens) != 0 {
		// (?i) for case insensitivity
		regx, err := regexp.Compile("(?i)" + tokens[0])
		if err != nil {
			send("list: "+err.Error(), ud.Message.Chat.ID)
			return
		}

		for i := range torrents {
			if regx.MatchString(torrents[i].GetTrackers()) {
				buf.WriteString(fmt.Sprintf("<%d> %s\n", torrents[i].ID, torrents[i].Name))
			}
		}
	} else { // if we did not get a query, list all torrents
		for i := range torrents {
			buf.WriteString(fmt.Sprintf("<%d> %s\n", torrents[i].ID, torrents[i].Name))
		}
	}

	if buf.Len() == 0 {
		send("list: No torrents", ud.Message.Chat.ID)
		return
	}

	send(buf.String(), ud.Message.Chat.ID)
}

// head will list the first 5 or n torrents
func head(ud tgbotapi.Update, tokens []string) {
	var (
		n   = 5 // default to 5
		err error
	)

	if len(tokens) > 0 {
		n, err = strconv.Atoi(tokens[0])
		if err != nil {
			send("head: argument must be a number", ud.Message.Chat.ID)
			return
		}
	}

	torrents, err := Client.GetTorrents()
	if err != nil {
		send("head: "+err.Error(), ud.Message.Chat.ID)
		return
	}

	// make sure that we stay in the boundaries
	if n <= 0 || n > len(torrents) {
		n = len(torrents)
	}

	buf := new(bytes.Buffer)
	for i := range torrents[:n] {
		buf.WriteString(fmt.Sprintf("<%d> %s\n", torrents[i].ID, torrents[i].Name))
	}

	if buf.Len() == 0 {
		send("head: No torrents", ud.Message.Chat.ID)
		return
	}
	send(buf.String(), ud.Message.Chat.ID)

}

// tail lists the last 5 or n torrents
func tail(ud tgbotapi.Update, tokens []string) {
	var (
		n   = 5 // default to 5
		err error
	)

	if len(tokens) > 0 {
		n, err = strconv.Atoi(tokens[0])
		if err != nil {
			send("tail: argument must be a number", ud.Message.Chat.ID)
			return
		}
	}

	torrents, err := Client.GetTorrents()
	if err != nil {
		send("tail: "+err.Error(), ud.Message.Chat.ID)
		return
	}

	// make sure that we stay in the boundaries
	if n <= 0 || n > len(torrents) {
		n = len(torrents)
	}

	buf := new(bytes.Buffer)
	for _, torrent := range torrents[len(torrents)-n:] {
		buf.WriteString(fmt.Sprintf("<%d> %s\n", torrent.ID, torrent.Name))
	}

	if buf.Len() == 0 {
		send("tail: No torrents", ud.Message.Chat.ID)
		return
	}
	send(buf.String(), ud.Message.Chat.ID)
}

// downs will send the names of torrents with status 'Downloading' or in queue to
func downs(ud tgbotapi.Update) {
	torrents, err := Client.GetTorrents()
	if err != nil {
		send("downs: "+err.Error(), ud.Message.Chat.ID)
		return
	}

	buf := new(bytes.Buffer)
	for i := range torrents {
		// Downloading or in queue to download
		if torrents[i].Status == transmission.StatusDownloading ||
			torrents[i].Status == transmission.StatusDownloadPending {
			buf.WriteString(fmt.Sprintf("<%d> %s\n", torrents[i].ID, torrents[i].Name))
		}
	}

	if buf.Len() == 0 {
		send("No downloads", ud.Message.Chat.ID)
		return
	}
	send(buf.String(), ud.Message.Chat.ID)
}

// seeding will send the names of the torrents with the status 'Seeding' or in the queue to
func seeding(ud tgbotapi.Update) {
	torrents, err := Client.GetTorrents()
	if err != nil {
		send("seeding: "+err.Error(), ud.Message.Chat.ID)
		return
	}

	buf := new(bytes.Buffer)
	for i := range torrents {
		if torrents[i].Status == transmission.StatusSeeding ||
			torrents[i].Status == transmission.StatusSeedPending {
			buf.WriteString(fmt.Sprintf("<%d> %s\n", torrents[i].ID, torrents[i].Name))
		}
	}

	if buf.Len() == 0 {
		send("No torrents seeding", ud.Message.Chat.ID)
		return
	}

	send(buf.String(), ud.Message.Chat.ID)

}

// paused will send the names of the torrents with status 'Paused'
func paused(ud tgbotapi.Update) {
	torrents, err := Client.GetTorrents()
	if err != nil {
		send("paused: "+err.Error(), ud.Message.Chat.ID)
		return
	}

	buf := new(bytes.Buffer)
	for i := range torrents {
		if torrents[i].Status == transmission.StatusStopped {
			buf.WriteString(fmt.Sprintf("<%d> %s\n%s (%.1f%%) DL: %s UL: %s  R: %s\n\n",
				torrents[i].ID, torrents[i].Name, torrents[i].TorrentStatus(),
				torrents[i].PercentDone*100, humanize.Bytes(torrents[i].DownloadedEver),
				humanize.Bytes(torrents[i].UploadedEver), torrents[i].Ratio()))
		}
	}

	if buf.Len() == 0 {
		send("No paused torrents", ud.Message.Chat.ID)
		return
	}

	send(buf.String(), ud.Message.Chat.ID)
}

// checking will send the names of torrents with the status 'verifying' or in the queue to
func checking(ud tgbotapi.Update) {
	torrents, err := Client.GetTorrents()
	if err != nil {
		send("checking: "+err.Error(), ud.Message.Chat.ID)
		return
	}

	buf := new(bytes.Buffer)
	for i := range torrents {
		if torrents[i].Status == transmission.StatusChecking ||
			torrents[i].Status == transmission.StatusCheckPending {
			buf.WriteString(fmt.Sprintf("<%d> %s\n%s (%.1f%%)\n\n",
				torrents[i].ID, torrents[i].Name, torrents[i].TorrentStatus(),
				torrents[i].PercentDone*100))

		}
	}

	if buf.Len() == 0 {
		send("No torrents verifying", ud.Message.Chat.ID)
		return
	}

	send(buf.String(), ud.Message.Chat.ID)
}

// active will send torrents that are actively downloading or uploading
func active(ud tgbotapi.Update) {
	torrents, err := Client.GetTorrents()
	if err != nil {
		send("active: "+err.Error(), ud.Message.Chat.ID)
		return
	}

	buf := new(bytes.Buffer)
	for i := range torrents {
		if torrents[i].RateDownload > 0 ||
			torrents[i].RateUpload > 0 {
			buf.WriteString(fmt.Sprintf("<%d> %s\n%s %s of %s (%.1f%%) ↓ %s  ↑ %s, R: %s\n\n",
				torrents[i].ID, torrents[i].Name, torrents[i].TorrentStatus(), humanize.Bytes(torrents[i].Have()),
				humanize.Bytes(torrents[i].SizeWhenDone), torrents[i].PercentDone*100, humanize.Bytes(torrents[i].RateDownload),
				humanize.Bytes(torrents[i].RateUpload), torrents[i].Ratio()))
		}
	}
	if buf.Len() == 0 {
		send("No active torrents", ud.Message.Chat.ID)
		return
	}

	msgID := send(buf.String(), ud.Message.Chat.ID)

	// keep the active list live for 'duration * interval'
	time.Sleep(time.Second * interval)
	for i := 0; i < duration; i++ {
		// reset the buffer to reuse it
		buf.Reset()

		// update torrents
		torrents, err = Client.GetTorrents()
		if err != nil {
			continue // if there was error getting torrents, skip to the next iteration
		}

		// do the same loop again
		for i := range torrents {
			if torrents[i].RateDownload > 0 ||
				torrents[i].RateUpload > 0 {
				buf.WriteString(fmt.Sprintf("<%d> %s\n%s %s of %s (%.1f%%) ↓ %s  ↑ %s, R: %s\n\n",
					torrents[i].ID, torrents[i].Name, torrents[i].TorrentStatus(), humanize.Bytes(torrents[i].Have()),
					humanize.Bytes(torrents[i].SizeWhenDone), torrents[i].PercentDone*100, humanize.Bytes(torrents[i].RateDownload),
					humanize.Bytes(torrents[i].RateUpload), torrents[i].Ratio()))
			}
		}

		// no need to check if it is empty, as if the buffer is empty telegram won't change the message
		editConf := tgbotapi.NewEditMessageText(ud.Message.Chat.ID, msgID, buf.String())
		Bot.Send(editConf)
		time.Sleep(time.Second * interval)
	}

	// replace the speed with dashes to indicate that we are done being live
	buf.Reset()
	for i := range torrents {
		if torrents[i].RateDownload > 0 ||
			torrents[i].RateUpload > 0 {
			buf.WriteString(fmt.Sprintf("<%d> %s\n%s %s of %s (%.1f%%) ↓ - B  ↑ - B, R: %s\n\n",
				torrents[i].ID, torrents[i].Name, torrents[i].TorrentStatus(), humanize.Bytes(torrents[i].Have()),
				humanize.Bytes(torrents[i].SizeWhenDone), torrents[i].PercentDone*100, torrents[i].Ratio()))
		}
	}

	editConf := tgbotapi.NewEditMessageText(ud.Message.Chat.ID, msgID, buf.String())
	Bot.Send(editConf)

}

// errors will send torrents with errors
func errors(ud tgbotapi.Update) {
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

// sort changes torrents sorting
func sort(ud tgbotapi.Update, tokens []string) {
	if len(tokens) == 0 {
		send(`sort takes one of (id, name, age, size, progress, downspeed, upspeed, download, upload, ratio)
			optionally start with (rev) for reversed order
			e.g. "sort rev size" to get biggest torrents first.`, ud.Message.Chat.ID)
		return
	}

	var reversed bool
	if strings.ToLower(tokens[0]) == "rev" {
		reversed = true
		tokens = tokens[1:]
	}

	switch strings.ToLower(tokens[0]) {
	case "id":
		if reversed {
			Client.SetSort(transmission.SortRevID)
			break
		}
		Client.SetSort(transmission.SortID)
	case "name":
		if reversed {
			Client.SetSort(transmission.SortRevName)
			break
		}
		Client.SetSort(transmission.SortName)
	case "age":
		if reversed {
			Client.SetSort(transmission.SortRevAge)
			break
		}
		Client.SetSort(transmission.SortAge)
	case "size":
		if reversed {
			Client.SetSort(transmission.SortRevSize)
			break
		}
		Client.SetSort(transmission.SortSize)
	case "progress":
		if reversed {
			Client.SetSort(transmission.SortRevProgress)
			break
		}
		Client.SetSort(transmission.SortProgress)
	case "downspeed":
		if reversed {
			Client.SetSort(transmission.SortRevDownSpeed)
			break
		}
		Client.SetSort(transmission.SortDownSpeed)
	case "upspeed":
		if reversed {
			Client.SetSort(transmission.SortRevUpSpeed)
			break
		}
		Client.SetSort(transmission.SortUpSpeed)
	case "download":
		if reversed {
			Client.SetSort(transmission.SortRevDownloaded)
			break
		}
		Client.SetSort(transmission.SortDownloaded)
	case "upload":
		if reversed {
			Client.SetSort(transmission.SortRevUploaded)
			break
		}
		Client.SetSort(transmission.SortUploaded)
	case "ratio":
		if reversed {
			Client.SetSort(transmission.SortRevRatio)
			break
		}
		Client.SetSort(transmission.SortRatio)
	default:
		send("unkown sorting method", ud.Message.Chat.ID)
		return
	}

	if reversed {
		send("sort: reversed "+tokens[0], ud.Message.Chat.ID)
		return
	}
	send("sort: "+tokens[0], ud.Message.Chat.ID)
}

var trackerRegex = regexp.MustCompile(`[https?|udp]://([^:/]*)`)

// trackers will send a list of trackers and how many torrents each one has
func trackers(ud tgbotapi.Update) {
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
func add(ud tgbotapi.Update, tokens []string) {
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
		send(fmt.Sprintf("Added: <%d> %s", torrent.ID, torrent.Name), ud.Message.Chat.ID)
	}
}

// receiveTorrent gets an update that potentially has a .torrent file to add
func receiveTorrent(ud tgbotapi.Update) {
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
func search(ud tgbotapi.Update, tokens []string) {
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
func latest(ud tgbotapi.Update, tokens []string) {
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
	if n <= 0 || n > len(torrents) {
		n = len(torrents)
	}

	// sort by age, and set reverse to true to get the latest first
	torrents.SortAge(true)

	buf := new(bytes.Buffer)
	for i := range torrents[:n] {
		buf.WriteString(fmt.Sprintf("<%d> %s\n", torrents[i].ID, torrents[i].Name))
	}
	if buf.Len() == 0 {
		send("latest: No torrents", ud.Message.Chat.ID)
		return
	}
	send(buf.String(), ud.Message.Chat.ID)
}

// info takes an id of a torrent and returns some info about it
func info(ud tgbotapi.Update, tokens []string) {
	if len(tokens) == 0 {
		send("info: needs a torrent ID number", ud.Message.Chat.ID)
		return
	}

	for _, id := range tokens {
		torrentID, err := strconv.Atoi(id)
		if err != nil {
			send(fmt.Sprintf("info: %s is not a number", id), ud.Message.Chat.ID)
			continue
		}

		// get the torrent
		torrent, err := Client.GetTorrent(torrentID)
		if err != nil {
			send(fmt.Sprintf("info: Can't find a torrent with an ID of %d", torrentID), ud.Message.Chat.ID)
			continue
		}

		// get the trackers using 'trackerRegex'
		var trackers string
		for _, tracker := range torrent.Trackers {
			sm := trackerRegex.FindSubmatch([]byte(tracker.Announce))
			if len(sm) > 1 {
				trackers += string(sm[1]) + " "
			}
		}

		// format the info
		info := fmt.Sprintf("<%d> %s\n%s\t%s of %s (%.1f%%) ↓ %s  ↑ %s, R: %s\nDL: %s, UP: %s\nAdded: %s, ETA: %s\nTrackers: %s",
			torrent.ID, torrent.Name, torrent.TorrentStatus(), humanize.Bytes(torrent.Have()), humanize.Bytes(torrent.SizeWhenDone),
			torrent.PercentDone*100, humanize.Bytes(torrent.RateDownload), humanize.Bytes(torrent.RateUpload), torrent.Ratio(),
			humanize.Bytes(torrent.DownloadedEver), humanize.Bytes(torrent.UploadedEver), time.Unix(torrent.AddedDate, 0).Format(time.Stamp),
			torrent.ETA(), trackers)

		// send it
		msgID := send(info, ud.Message.Chat.ID)

		// this go-routine will make the info live for 'duration * interval'
		// takes trackers so we don't have to regex them over and over.
		go func(trackers string, torrentID, msgID int) {
			for i := 0; i < duration; i++ {
				torrent, err := Client.GetTorrent(torrentID)
				if err != nil {
					continue // skip this iteration if there's an error retrieving the torrent's info
				}

				info := fmt.Sprintf("<%d> %s\n%s\t%s of %s (%.1f%%) ↓ %s  ↑ %s, R: %s\nDL: %s, UP: %s\nAdded: %s, ETA: %s\nTrackers: %s",
					torrent.ID, torrent.Name, torrent.TorrentStatus(), humanize.Bytes(torrent.Have()), humanize.Bytes(torrent.SizeWhenDone),
					torrent.PercentDone*100, humanize.Bytes(torrent.RateDownload), humanize.Bytes(torrent.RateUpload), torrent.Ratio(),
					humanize.Bytes(torrent.DownloadedEver), humanize.Bytes(torrent.UploadedEver), time.Unix(torrent.AddedDate, 0).Format(time.Stamp),
					torrent.ETA(), trackers)

				// update the message
				editConf := tgbotapi.NewEditMessageText(ud.Message.Chat.ID, msgID, info)
				Bot.Send(editConf)
				time.Sleep(time.Second * interval)

			}

			// at the end write dashes to indicate that we are done being live.
			info := fmt.Sprintf("<%d> %s\n%s\t%s of %s (%.1f%%) ↓ - B  ↑ - B, R: %s\nDL: %s, UP: %s\nAdded: %s, ETA: -\nTrackers: %s",
				torrent.ID, torrent.Name, torrent.TorrentStatus(), humanize.Bytes(torrent.Have()), humanize.Bytes(torrent.SizeWhenDone),
				torrent.PercentDone*100, torrent.Ratio(), humanize.Bytes(torrent.DownloadedEver), humanize.Bytes(torrent.UploadedEver),
				time.Unix(torrent.AddedDate, 0).Format(time.Stamp), trackers)

			editConf := tgbotapi.NewEditMessageText(ud.Message.Chat.ID, msgID, info)
			Bot.Send(editConf)
		}(trackers, torrentID, msgID)
	}
}

// stop takes id[s] of torrent[s] or 'all' to stop them
func stop(ud tgbotapi.Update, tokens []string) {
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
func start(ud tgbotapi.Update, tokens []string) {
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
func check(ud tgbotapi.Update, tokens []string) {
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
func stats(ud tgbotapi.Update) {
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
func speed(ud tgbotapi.Update) {
	// keep track of the returned message ID from 'send()' to edit the message.
	var msgID int
	for i := 0; i < duration; i++ {
		stats, err := Client.GetStats()
		if err != nil {
			send("speed: "+err.Error(), ud.Message.Chat.ID)
			return
		}

		msg := fmt.Sprintf("↓ %s  ↑ %s", humanize.Bytes(stats.DownloadSpeed), humanize.Bytes(stats.UploadSpeed))

		// if we haven't send a message, send it and save the message ID to edit it the next iteration
		if msgID == 0 {
			msgID = send(msg, ud.Message.Chat.ID)
			time.Sleep(time.Second * interval)
			continue
		}

		// we have sent the message, let's update.
		editConf := tgbotapi.NewEditMessageText(ud.Message.Chat.ID, msgID, msg)
		Bot.Send(editConf)
		time.Sleep(time.Second * interval)
	}

	// after the 10th iteration, show dashes to indicate that we are done updating.
	editConf := tgbotapi.NewEditMessageText(ud.Message.Chat.ID, msgID, "↓ - B  ↑ - B")
	Bot.Send(editConf)
}

// count returns current torrents count per status
func count(ud tgbotapi.Update) {
	torrents, err := Client.GetTorrents()
	if err != nil {
		send("count: "+err.Error(), ud.Message.Chat.ID)
		return
	}

	var downloading, seeding, stopped, checking, downloadingQ, seedingQ, checkingQ int

	for i := range torrents {
		switch torrents[i].Status {
		case transmission.StatusDownloading:
			downloading++
		case transmission.StatusSeeding:
			seeding++
		case transmission.StatusStopped:
			stopped++
		case transmission.StatusChecking:
			checking++
		case transmission.StatusDownloadPending:
			downloadingQ++
		case transmission.StatusSeedPending:
			seedingQ++
		case transmission.StatusCheckPending:
			checkingQ++
		}
	}

	msg := fmt.Sprintf("Downloading: %d\nSeeding: %d\nPaused: %d\nVerifying: %d\n\n- Waiting to -\nDownload: %d\nSeed: %d\nVerify: %d\n\nTotal: %d",
		downloading, seeding, stopped, checking, downloadingQ, seedingQ, checkingQ, len(torrents))

	send(msg, ud.Message.Chat.ID)

}

// del takes an id or more, and delete the corresponding torrent/s
func del(ud tgbotapi.Update, tokens []string) {
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
func deldata(ud tgbotapi.Update, tokens []string) {
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
func version(ud tgbotapi.Update) {
	send(fmt.Sprintf("Transmission %s\nTransmission-telegram %s", Client.Version(), VERSION), ud.Message.Chat.ID)
}

// send takes a chat id and a message to send, returns the message id of the send message
func send(text string, chatID int64) int {
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
			fmt.Fprintf(os.Stderr, "send error: %s\n", err.Error())
		}
		// move to the next chunk
		text = text[4095:]
		msgRuneCount = utf8.RuneCountInString(text)
		goto LenCheck
	}

	// if msgRuneCount < 4096, send it normally
	msg := tgbotapi.NewMessage(chatID, text)
	msg.DisableWebPagePreview = true

	resp, err := Bot.Send(msg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "send error: %s\n", err.Error())
	}

	return resp.MessageID
}
