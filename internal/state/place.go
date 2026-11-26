package state

import (
	"cmp"
	"fmt"
	"strconv"
	"strings"
)

// Place is where in Telegram the bot sends: a chat and, in a group with
// topics (a forum), one of its topics. Topic 0 is the chat itself, or its
// General topic, which messages reach without naming a topic.
type Place struct {
	Chat  int64
	Topic int
}

// MarshalText writes a place as the state file's keys do: the chat's ID,
// as before topics, followed by a colon and the topic when there is one.
func (p Place) MarshalText() ([]byte, error) {
	return []byte(p.String()), nil
}

// UnmarshalText reads a place as MarshalText writes it.
func (p *Place) UnmarshalText(text []byte) error {
	chat, topic, hasTopic := strings.Cut(string(text), ":")
	id, err := strconv.ParseInt(chat, 10, 64)
	if err != nil {
		return fmt.Errorf("%q is not a chat", text)
	}
	place := Place{Chat: id}
	if hasTopic {
		if place.Topic, err = strconv.Atoi(topic); err != nil || place.Topic <= 0 {
			return fmt.Errorf("%q is not a chat's topic", text)
		}
	}
	*p = place
	return nil
}

func (p Place) String() string {
	chat := strconv.FormatInt(p.Chat, 10)
	if p.Topic == 0 {
		return chat
	}
	return chat + ":" + strconv.Itoa(p.Topic)
}

// Compare orders places by chat, then topic.
func (p Place) Compare(other Place) int {
	return cmp.Or(cmp.Compare(p.Chat, other.Chat), cmp.Compare(p.Topic, other.Topic))
}
