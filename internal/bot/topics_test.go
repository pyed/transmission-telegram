package bot

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/pyed/transmission"
	"github.com/pyed/transmission-telegram/v2/internal/config"
	"github.com/pyed/transmission-telegram/v2/internal/notify"
	"github.com/pyed/transmission-telegram/v2/internal/state"
)

// threadNotFound is Telegram's answer to a message for a topic that was
// deleted.
const threadNotFound = `{"ok":false,"error_code":400,"description":"Bad Request: message thread not found"}`

// A command typed in a topic is answered there, and the topic is where
// notifications go by default. Messages in the General topic, and replies
// in a group without topics, which carry a thread ID too, are answered
// naming no topic, as Telegram wants.
func TestRepliesStayInTheirTopic(t *testing.T) {
	ft := newFakeTelegram(t)
	b := newTestBot(t, ft.newBot(t), nil)
	b.Config.Masters = config.MasterSlice{"12345"}
	ctx := context.Background()

	b.handleUpdate(ctx, nil, messageInTopic(-100, 5, "/nosuch"))
	b.State.Read(func(data *state.Data) {
		if data.Last() != (state.Place{Chat: -100, Topic: 5}) {
			t.Errorf("the last place used is %v, want topic 5 of chat -100", data.Last())
		}
	})

	general := messageIn(-100, "/nosuch")
	general.Message.Chat.IsForum = true
	replyThread := messageIn(-200, "/nosuch")
	replyThread.Message.MessageThreadID = 77
	for _, update := range []*models.Update{general, replyThread, messageInTopic(-100, generalTopic, "/nosuch")} {
		b.handleUpdate(ctx, nil, update)
	}
	if got, want := ft.sentTopics(), []string{"5", "", "", ""}; !slices.Equal(got, want) {
		t.Fatalf("replies went to topics %q, want %q", got, want)
	}
	if got, want := ft.sentChats(), []string{"-100", "-100", "-200", "-100"}; !slices.Equal(got, want) {
		t.Fatalf("replies went to chats %q, want %q", got, want)
	}
}

// Commands that hand their work on, as /deldata ... confirm and /queue do,
// still answer in the topic.
func TestHandedOnCommandsAnswerInTheirTopic(t *testing.T) {
	b, ft, _ := buttonBot(t)
	ctx := context.Background()
	for _, command := range []string{"/deldata 99 confirm", "/queue top 99", "/move 99 /films", "/setlabel 99 films"} {
		b.handleUpdate(ctx, nil, messageInTopic(-100, 5, command))
	}
	topics := ft.sentTopics()
	if len(topics) < 4 {
		t.Fatalf("%d replies: %q", len(topics), ft.sentTexts())
	}
	for i, topic := range topics {
		if topic != "5" {
			t.Errorf("reply %q went to topic %q, want 5", ft.sentTexts()[i], topic)
		}
	}
}

// A reply too long for messages is attached as a file in the topic.
func TestLongRepliesAreAttachedInTheTopic(t *testing.T) {
	ft := newFakeTelegram(t)
	b := newTestBot(t, ft.newBot(t), nil)
	long := strings.Repeat(strings.Repeat("a", 99)+"\n", 200)
	if _, err := b.send(context.Background(), state.Place{Chat: -100, Topic: 5}, long, nil); err != nil {
		t.Fatal(err)
	}
	if got := ft.sentDocumentTopics(); !slices.Equal(got, []string{"5"}) {
		t.Fatalf("files went to topics %q, want 5", got)
	}
}

// While a command in a topic takes a while, the bot is shown typing there.
func TestTypingShowsInTheTopic(t *testing.T) {
	ft := newFakeTelegram(t)
	fts := newFakeTransmission(t)
	client, err := transmission.New(fts.URL+"/transmission/rpc", "", "")
	if err != nil {
		t.Fatal(err)
	}
	b := newTestBot(t, ft.newBot(t), client)
	b.Config.Masters = config.MasterSlice{"12345"}
	b.typingDelay = 10 * time.Millisecond
	stall := make(chan struct{})
	fts.setStall(stall)

	done := make(chan struct{})
	go func() {
		defer close(done)
		b.serveUpdate(context.Background(), messageInTopic(-100, 5, "/list"))
	}()
	deadline := time.Now().Add(5 * time.Second)
	for ft.callCount("sendChatAction") == 0 {
		if time.Now().After(deadline) {
			close(stall)
			t.Fatal("the bot was not shown typing")
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(stall)
	<-done
	b.wg.Wait()
	if got := ft.sentActionTopics(); !slices.Equal(got, []string{"5"}) {
		t.Fatalf("typing was shown in topics %q, want 5", got)
	}
}

// What a button sends goes to the pressed message's topic: here, the card
// a notification's Open button sends.
func TestButtonsSendInThePressedMessagesTopic(t *testing.T) {
	b, ft, _ := buttonBot(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		b.wg.Wait()
	}()
	press := pressFrom(12345, "t:7:"+torrentFingerprint(testHash))
	press.Message.Message.Chat = models.Chat{ID: -100, Type: models.ChatTypeSupergroup, IsForum: true}
	press.Message.Message.IsTopicMessage, press.Message.Message.MessageThreadID = true, 5
	b.handleCallbackQuery(ctx, press)
	ft.waitForSent(t, 1)
	if got := ft.sentTopics(); got[0] != "5" || !strings.Contains(ft.sentTexts()[0], "Test &lt;Torrent&gt;") {
		t.Fatalf("the card went to topic %q: %q", got[0], ft.sentTexts()[0])
	}
}

// /notify in a topic chooses that topic's notifications, apart from the
// chat's other topics, and notifications then go there.
func TestNotifyInATopic(t *testing.T) {
	ft := newFakeTelegram(t)
	b := newTestBot(t, ft.newBot(t), nil)
	ctx := context.Background()
	downloads, alerts := state.Place{Chat: -100, Topic: 5}, state.Place{Chat: -100, Topic: 6}
	if err := b.State.Update(func(data *state.Data) { data.SetLast(downloads) }); err != nil {
		t.Fatal(err)
	}

	b.notify(ctx, &request{chatID: -100, topic: 6})
	if text := ft.sentTexts()[0]; !strings.Contains(text, "Notifications in this topic") || !strings.Contains(text, "a master used last: another topic") {
		t.Errorf("/notify in another topic shows:\n%s", text)
	}
	b.notify(ctx, &request{chatID: -100, topic: 6, args: []string{"errors", "on"}})
	b.State.Read(func(data *state.Data) {
		want := map[state.Place][]string{downloads: notify.DefaultKinds, alerts: {notify.Errors}}
		if !maps.EqualFunc(data.Notify, want, slices.Equal) {
			t.Errorf("choices = %v, want %v", data.Notify, want)
		}
		if got := notify.Chats(data, notify.Errors); !slices.Equal(got, []state.Place{alerts}) {
			t.Errorf("errors go to %v", got)
		}
	})
	if err := b.SendHTML(ctx, alerts, "⚠️ <b>Error:</b> x", nil); err != nil {
		t.Fatal(err)
	}
	if got := ft.sentTopics(); !slices.Equal(got, []string{"6", "6", "6"}) {
		t.Fatalf("messages went to topics %q, want 6", got)
	}
}

// Each topic has a status message of its own, which its Refresh button
// finds even when Telegram no longer shows the bot the message; /status off
// ends only the topic's own.
func TestStatusInTopics(t *testing.T) {
	b, ft, _ := statusBot(t)
	ctx := context.Background()
	downloads, alerts := state.Place{Chat: -100, Topic: 5}, state.Place{Chat: -100, Topic: 6}

	b.status(ctx, &request{chatID: -100, topic: 5})
	b.status(ctx, &request{chatID: -100, topic: 6})
	b.State.Read(func(data *state.Data) {
		if want := map[state.Place]int{downloads: 1, alerts: 2}; !maps.Equal(data.Status, want) {
			t.Fatalf("status messages = %v, want %v", data.Status, want)
		}
	})
	if got := ft.sentTopics(); !slices.Equal(got, []string{"5", "6"}) || len(ft.sentPins()) != 2 {
		t.Fatalf("status messages went to topics %q, with %d pins", got, len(ft.sentPins()))
	}

	b.checkStatus(ctx)
	if len(ft.editsOf("1")) != 1 || len(ft.editsOf("2")) != 1 {
		t.Fatalf("checkStatus edited %q and %q", ft.editsOf("1"), ft.editsOf("2"))
	}
	refresh := pressFrom(12345, "sr")
	refresh.Message = models.MaybeInaccessibleMessage{
		Type:                models.MaybeInaccessibleMessageTypeInaccessibleMessage,
		InaccessibleMessage: &models.InaccessibleMessage{Chat: models.Chat{ID: -100}, MessageID: 2},
	}
	b.handleCallbackQuery(ctx, refresh)
	if answers := ft.callbackAnswers(); len(answers) != 1 || answers[0] != "Updated" {
		t.Fatalf("Refresh answered %q", answers)
	}

	b.status(ctx, &request{chatID: -100, topic: 5, args: []string{"off"}})
	b.State.Read(func(data *state.Data) {
		if want := map[state.Place]int{alerts: 2}; !maps.Equal(data.Status, want) {
			t.Fatalf("after /status off in topic 5, status messages = %v, want %v", data.Status, want)
		}
	})
}

// A digest set in a topic comes in the topic.
func TestDigestInATopic(t *testing.T) {
	b, ft, _, now := digestBot(t)
	ctx := context.Background()
	downloads := state.Place{Chat: -100, Topic: 5}
	b.digest(ctx, &request{chatID: -100, topic: 5, args: []string{"09:00"}})
	*now = now.Add(time.Hour)
	b.checkDigest(ctx)
	if got := ft.sentTopics(); !slices.Equal(got, []string{"5", "5"}) || !strings.Contains(ft.sentTexts()[1], "Daily digest") {
		t.Fatalf("sent %q to topics %q", ft.sentTexts(), got)
	}
	b.State.Read(func(data *state.Data) {
		if d, ok := data.Digest[downloads]; !ok || d.LastDue != "2026-10-21" || len(data.Digest) != 1 {
			t.Fatalf("digests = %v", data.Digest)
		}
	})
}

// When a topic is deleted, what was set up in it is forgotten: its
// notifications stop, and go nowhere else, its digest and status end, and
// notifications by default no longer go to it. The chat's other topics
// keep theirs, and a topic that is only closed keeps its own.
func TestAGoneTopicIsForgotten(t *testing.T) {
	b, ft, _, _ := digestBot(t)
	ctx := context.Background()
	gone, other, third := state.Place{Chat: -100, Topic: 5}, state.Place{Chat: -100, Topic: 6}, state.Place{Chat: -100, Topic: 7}
	if err := b.State.Update(func(data *state.Data) {
		data.SetLast(gone)
		data.Notify = map[state.Place][]string{gone: {notify.Completed}, other: {notify.Errors}, third: {notify.Disk}}
		data.Status = map[state.Place]int{gone: 3}
		data.Digest = map[state.Place]state.Digest{gone: {Time: "09:00", LastDue: "2026-10-21"}, other: {Time: "08:00", LastDue: "2026-10-20"}}
	}); err != nil {
		t.Fatal(err)
	}

	ft.script("sendMessage", `{"ok":false,"error_code":400,"description":"Bad Request: TOPIC_CLOSED"}`)
	if err := b.SendHTML(ctx, gone, "✅ <b>Finished:</b> x", nil); err == nil {
		t.Fatal("a notification to a closed topic went through")
	}
	b.State.Read(func(data *state.Data) {
		if len(data.Notify[gone]) != 1 || len(data.Digest) != 2 || len(data.Status) != 1 || data.Last() != gone {
			t.Fatalf("a closed topic was forgotten: %+v", data)
		}
	})

	ft.script("sendMessage", threadNotFound)
	if err := b.SendHTML(ctx, gone, "✅ <b>Finished:</b> x", nil); err == nil {
		t.Fatal("a notification to a deleted topic went through")
	}
	if calls := ft.callCount("sendMessage"); calls != 2 || len(ft.sentTexts()) != 0 {
		t.Fatalf("%d messages were sent, %q delivered; want none, anywhere else", calls, ft.sentTexts())
	}
	b.State.Read(func(data *state.Data) {
		if kinds, ok := data.Notify[gone]; !ok || len(kinds) != 0 || len(data.Notify[other]) != 1 || len(data.Notify[third]) != 1 {
			t.Errorf("notifications after the topic is gone = %v", data.Notify)
		}
		if got := notify.Chats(data, notify.Completed); got != nil {
			t.Errorf("completions still go to %v", got)
		}
		if _, ok := data.Digest[gone]; ok || len(data.Digest) != 1 || len(data.Status) != 0 || data.Last() != (state.Place{}) {
			t.Errorf("after the topic is gone: %+v", data)
		}
	})

	// A digest due in a topic that is gone ends, with what else was set
	// up there.
	ft.script("sendMessage", threadNotFound)
	b.checkDigest(ctx)
	b.State.Read(func(data *state.Data) {
		if kinds := data.Notify[other]; len(data.Digest) != 0 || len(kinds) != 0 {
			t.Errorf("after the other topic is gone, digests = %v, its notifications %v", data.Digest, kinds)
		}
	})

	// So does a topic's choice of notifications, all it had, when a reply
	// too long for messages finds it gone.
	ft.script("sendDocument", threadNotFound)
	if _, err := b.send(ctx, third, strings.Repeat(strings.Repeat("a", 99)+"\n", 200), nil); err == nil {
		t.Fatal("a file to a deleted topic went through")
	}
	b.State.Read(func(data *state.Data) {
		if kinds, ok := data.Notify[third]; !ok || len(kinds) != 0 {
			t.Errorf("notifications after the third topic is gone = %v", data.Notify)
		}
	})
}
