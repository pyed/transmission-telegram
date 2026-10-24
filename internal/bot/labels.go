package bot

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// labelsRPCVersion is the RPC version of Transmission 3.00, the first with
// labels.
const labelsRPCVersion = 16

// labelsSupported tells the chat, and returns false, when Transmission is
// too old for labels.
func (b *Bot) labelsSupported(ctx context.Context, r *request, name string) bool {
	session, err := b.Client.GetSession(ctx)
	if err != nil {
		b.replyError(ctx, r, name, err)
		return false
	}
	if session.RPCVersion < labelsRPCVersion {
		b.reply(ctx, r, "<b>"+name+":</b> labels need Transmission 3.00 or newer")
		return false
	}
	return true
}

// labels counts the torrents with each label; with a label, it lists them,
// and with "-" those without one.
func (b *Bot) labels(ctx context.Context, r *request) {
	if !b.labelsSupported(ctx, r, "labels") {
		return
	}
	if len(r.args) > 0 {
		b.replyList(ctx, r, "labels")
		return
	}
	torrents, err := b.torrents(ctx, r.chatID)
	if err != nil {
		b.replyError(ctx, r, "labels", err)
		return
	}
	counts := make(map[string]int)
	var unlabelled int
	for _, t := range torrents {
		if len(t.Labels) == 0 {
			unlabelled++
		}
		for _, label := range t.Labels {
			counts[label]++
		}
	}
	if len(counts) == 0 {
		b.reply(ctx, r, "No torrent has a label. Give some with <code>/setlabel ID LABEL</code>.")
		return
	}
	labels := make([]string, 0, len(counts))
	for label := range counts {
		labels = append(labels, label)
	}
	slices.SortFunc(labels, func(a, b string) int {
		return cmp.Or(cmp.Compare(counts[b], counts[a]), cmp.Compare(strings.ToLower(a), strings.ToLower(b)), cmp.Compare(a, b))
	})
	lines := make([]string, 0, len(labels)+2)
	for _, label := range labels {
		lines = append(lines, fmt.Sprintf("<b>%d</b> %s", counts[label], esc(label)))
	}
	if unlabelled > 0 {
		lines = append(lines, fmt.Sprintf("<b>%d</b> <i>without a label</i>", unlabelled))
	}
	lines = append(lines, "", "List a label's torrents with <code>/labels NAME</code>, or those without one with <code>/labels -</code>.")
	b.reply(ctx, r, strings.Join(lines, "\n"))
}

// setlabel sets torrents' labels: /setlabel <id…> <label…>, or "-" for no
// label. The leading arguments that are numbers are IDs, and the rest are
// labels.
func (b *Bot) setlabel(ctx context.Context, r *request) {
	ids := 0
	for ids < len(r.args) {
		if _, err := strconv.Atoi(r.args[ids]); err != nil {
			break
		}
		ids++
	}
	labels := r.args[ids:]
	if ids == 0 || len(labels) == 0 {
		b.reply(ctx, r, "<b>setlabel:</b> give the torrents' IDs and then their labels, or - for none: <code>/setlabel 5 7 movies</code>")
		return
	}
	if len(labels) == 1 && labels[0] == "-" {
		labels = []string{}
	}
	for _, label := range labels {
		if strings.Contains(label, ",") {
			b.reply(ctx, r, "<b>setlabel:</b> a label may not contain a comma")
			return
		}
	}
	if !b.labelsSupported(ctx, r, "setlabel") {
		return
	}
	torrents, problems, ok := b.torrentsNamed(ctx, &request{chatID: r.chatID, args: r.args[:ids]}, "setlabel")
	if !ok {
		return
	}
	if len(torrents) > 0 {
		// pyed/transmission's SetTorrent leaves out an empty list of labels,
		// which is how they are removed.
		args := struct {
			IDs    []int    `json:"ids"`
			Labels []string `json:"labels"`
		}{torrents.GetIDs(), labels}
		if err := b.Client.Execute(ctx, "torrent-set", args, nil); err != nil {
			b.replyError(ctx, r, "setlabel", err)
			return
		}
	}
	done := "Labels removed"
	if len(labels) > 0 {
		done = "Labelled " + esc(strings.Join(labels, ", "))
	}
	b.reply(ctx, r, summary(done, torrents, problems))
}
