package bot

import (
	"errors"
	"strings"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/pyed/transmission-telegram/v2/internal/state"
)

// In a group with topics (a forum), each topic is like a chat of its own:
// the bot answers in the topic a command or button came from, and
// notifications, status messages and digests go to the topic they were set
// up in (state.Place). Messages in the General topic, or in a chat without
// topics, name no topic.

// generalTopic is the ID of a forum's General topic, which Telegram refuses
// as a topic to send to: messages for it name none.
const generalTopic = 1

// topicOf returns the forum topic a message is in, which the bot's answers
// go to: 0 outside topics, in the General topic, and in a reply thread of a
// group without topics, whose messages carry a thread ID too.
func topicOf(message *models.Message) int {
	if message == nil || !message.IsTopicMessage || message.MessageThreadID <= generalTopic {
		return 0
	}
	return message.MessageThreadID
}

// placeWord names a place in replies about it: "topic" or "chat".
func placeWord(place state.Place) string {
	if place.Topic != 0 {
		return "topic"
	}
	return "chat"
}

// topicGone reports whether err says the topic a message was sent to does
// not exist: it was deleted, and with it everything sent there.
func topicGone(err error) bool {
	return errors.Is(err, tgbot.ErrorBadRequest) && strings.Contains(strings.ToLower(err.Error()), "message thread not found")
}

// forgetGoneTopic forgets what was set up in a topic, when err says the
// topic is gone: its notifications stop, rather than going to another
// topic, and its digest and status message end.
func (b *Bot) forgetGoneTopic(to state.Place, err error) {
	if to.Topic == 0 || !topicGone(err) {
		return
	}
	var remembered bool
	b.State.Read(func(data *state.Data) {
		_, digest := data.Digest[to]
		_, status := data.Status[to]
		remembered = len(data.Notify[to]) > 0 || digest || status || data.Last() == to
	})
	if !remembered {
		return
	}
	b.Logger.Info("A topic is gone; forgetting what was set up in it", "chat_id", to.Chat, "topic", to.Topic)
	if err := b.State.Update(func(data *state.Data) { data.ForgetTopic(to) }); err != nil {
		b.Logger.Warn("Could not save that the topic is gone", "error", err)
	}
}
