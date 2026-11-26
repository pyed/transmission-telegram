package bot

import (
	"context"
	"errors"
	"html"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/pyed/transmission-telegram/v2/internal/state"
)

const (
	// maxMessageLength is Telegram's limit on a message's text: UTF-16 code
	// units, counted after HTML is parsed.
	maxMessageLength = 4096
	// maxMessageChunks is the most messages a reply is split into; a longer
	// one is attached as a file instead, which also spares the chat a burst
	// of messages and Telegram's flood limits.
	maxMessageChunks = 3
	// maxTelegramAttempts is how many times a request is made while Telegram
	// fails in a way that may pass.
	maxTelegramAttempts = 4
	// maxRetryAfter is the longest rate-limit wait the bot sits out before
	// giving up on a request.
	maxRetryAfter = time.Minute
)

// noPreview keeps links in replies from unfolding into previews.
var noPreview = &models.LinkPreviewOptions{IsDisabled: tgbot.True()}

// esc escapes text for Telegram's HTML parse mode. Every piece of text that
// is not the bot's own markup goes through it: torrent names, paths, errors,
// and whatever users typed.
func esc(text string) string {
	return html.EscapeString(text)
}

// htmlTag matches the tags the bot writes, which never contain ">".
var htmlTag = regexp.MustCompile(`<[^>]*>`)

// plainText returns an HTML message as the text Telegram shows for it.
func plainText(htmlText string) string {
	return html.UnescapeString(htmlTag.ReplaceAllString(htmlText, ""))
}

// telegramLength is the length of text as Telegram measures its limits, in
// UTF-16 code units: a character outside the Basic Multilingual Plane, as
// most emoji are, counts twice.
func telegramLength(text string) int {
	length := 0
	for _, r := range text {
		length += max(utf16.RuneLen(r), 1)
	}
	return length
}

// telegramPrefix returns the longest start of text that is at most limit
// long, as telegramLength measures.
func telegramPrefix(text string, limit int) string {
	length := 0
	for i, r := range text {
		if length += max(utf16.RuneLen(r), 1); length > limit {
			return text[:i]
		}
	}
	return text
}

// renderedLength is the length of an HTML message as Telegram counts it.
func renderedLength(htmlText string) int {
	return telegramLength(plainText(htmlText))
}

// chunkMessage splits an HTML message into messages Telegram accepts,
// between lines. The bot's markup never spans lines, so every chunk is valid
// HTML. A single line too long for a message is cut as plain text.
func chunkMessage(text string) []string {
	var chunks []string
	var current strings.Builder
	length := 0
	flush := func() {
		if chunk := strings.TrimRight(current.String(), "\n"); chunk != "" {
			chunks = append(chunks, chunk)
		}
		current.Reset()
		length = 0
	}
	for _, line := range strings.SplitAfter(text, "\n") {
		lineLength := renderedLength(line)
		if lineLength > maxMessageLength {
			flush()
			plain := plainText(line)
			for telegramLength(plain) > maxMessageLength {
				part := telegramPrefix(plain, maxMessageLength)
				chunks = append(chunks, esc(part))
				plain = plain[len(part):]
			}
			current.WriteString(esc(plain))
			length = telegramLength(plain)
			continue
		}
		if length+lineLength > maxMessageLength {
			flush()
		}
		current.WriteString(line)
		length += lineLength
	}
	flush()
	return chunks
}

// send sends an HTML message to a chat, in its topic if to names one, with
// markup under its last part. A long message goes as several, split
// between lines, and a very long one as an attached text file. It returns
// the message's ID, or 0 when the text took more than one message, which
// is then not to be edited.
func (b *Bot) send(ctx context.Context, to state.Place, text string, markup models.ReplyMarkup) (int, error) {
	messageID, _, err := b.sendIn(ctx, to, text, markup)
	return messageID, err
}

// sendIn is send, and also returns the place the message went to: in
// another chat than to's when that group became a supergroup, which is a
// new chat.
//
// A message with buttons is always one message, cut short if need be: a
// reply in several parts cannot be edited, and its buttons would not work.
func (b *Bot) sendIn(ctx context.Context, to state.Place, text string, markup models.ReplyMarkup) (int, state.Place, error) {
	if markup != nil {
		text = fitMessage(text)
	}
	chunks := chunkMessage(text)
	if len(chunks) == 0 {
		return 0, to, errors.New("refusing to send an empty message")
	}
	if len(chunks) > maxMessageChunks {
		to, err := b.sendFile(ctx, to, plainText(text))
		return 0, to, err
	}
	var messageID int
	for i, chunk := range chunks {
		params := &tgbot.SendMessageParams{
			ChatID:             to.Chat,
			MessageThreadID:    to.Topic,
			Text:               chunk,
			ParseMode:          models.ParseModeHTML,
			LinkPreviewOptions: noPreview,
		}
		if i == len(chunks)-1 && markup != nil {
			params.ReplyMarkup = markup
		}
		message, err := b.sendMessage(ctx, params)
		if id, ok := params.ChatID.(int64); ok {
			to.Chat = id // the chat it went to
		}
		if err != nil {
			b.Logger.Error("Sending a message failed", "chat_id", to.Chat, "topic", to.Topic, "error", err)
			return 0, to, err
		}
		messageID = message.ID
	}
	if len(chunks) > 1 {
		return 0, to, nil
	}
	return messageID, to, nil
}

// sendMessage sends one message, retrying passing failures, and following
// a group that became a supergroup to its new chat, which params then
// names. If Telegram cannot parse its HTML, which only a bug in the bot
// causes, it goes again as the plain text it was meant to show. When its
// topic is gone, what was set up there is forgotten.
func (b *Bot) sendMessage(ctx context.Context, params *tgbot.SendMessageParams) (*models.Message, error) {
	message, err := b.sendMessageTo(ctx, params)
	if to, ok := b.followMigration(params.ChatID, err); ok {
		params.ChatID = to
		message, err = b.sendMessageTo(ctx, params)
	}
	if chat, ok := params.ChatID.(int64); ok {
		b.forgetGoneTopic(state.Place{Chat: chat, Topic: params.MessageThreadID}, err)
	}
	return message, err
}

func (b *Bot) sendMessageTo(ctx context.Context, params *tgbot.SendMessageParams) (*models.Message, error) {
	var message *models.Message
	err := b.retryTelegram(ctx, func() (err error) {
		message, err = b.API.SendMessage(ctx, params)
		return err
	})
	if err != nil && params.ParseMode != "" && parseError(err) {
		b.Logger.Warn("Telegram could not parse a message's HTML; sending it as plain text", "error", err)
		plain := *params
		plain.ParseMode = ""
		plain.Text = plainText(params.Text)
		err = b.retryTelegram(ctx, func() (err error) {
			message, err = b.API.SendMessage(ctx, &plain)
			return err
		})
	}
	return message, err
}

// sendFile attaches text as a file, for replies too long to read as
// messages, and returns the place it went to.
func (b *Bot) sendFile(ctx context.Context, to state.Place, text string) (state.Place, error) {
	attach := func() error {
		return b.retryTelegram(ctx, func() error {
			_, err := b.API.SendDocument(ctx, &tgbot.SendDocumentParams{
				ChatID:          to.Chat,
				MessageThreadID: to.Topic,
				Document:        &models.InputFileUpload{Filename: "transmission-telegram.txt", Data: strings.NewReader(text)},
				Caption:         "This reply is too long for chat messages, so it is attached as a file.",
			})
			return err
		})
	}
	err := attach()
	if chat, ok := b.followMigration(to.Chat, err); ok {
		to.Chat = chat
		err = attach()
	}
	b.forgetGoneTopic(to, err)
	if err != nil {
		b.Logger.Error("Sending a file failed", "chat_id", to.Chat, "topic", to.Topic, "error", err)
	}
	return to, err
}

// fitMessage returns an HTML message cut to fit one Telegram message,
// between lines, ending with an ellipsis when it was cut. The bot's markup
// never spans lines, so the result is valid HTML.
func fitMessage(text string) string {
	if renderedLength(text) <= maxMessageLength {
		return text
	}
	const more = "\n…"
	lines := strings.Split(text, "\n")
	length := 0
	for i, line := range lines {
		added := renderedLength(line)
		if i > 0 {
			added++ // the line break
		}
		if length+added+telegramLength(more) > maxMessageLength {
			if i == 0 {
				return esc(telegramPrefix(plainText(line), maxMessageLength-1)) + "…"
			}
			return strings.Join(lines[:i], "\n") + more
		}
		length += added
	}
	return text
}

// edit replaces a message's HTML text and markup, cut to fit one message.
// An edit that changes nothing succeeds.
func (b *Bot) edit(ctx context.Context, chatID int64, messageID int, text string, markup models.ReplyMarkup) error {
	text = fitMessage(text)
	params := &tgbot.EditMessageTextParams{
		ChatID:             chatID,
		MessageID:          messageID,
		Text:               text,
		ParseMode:          models.ParseModeHTML,
		LinkPreviewOptions: noPreview,
		ReplyMarkup:        markup,
	}
	err := b.retryTelegram(ctx, func() error {
		_, err := b.API.EditMessageText(ctx, params)
		return err
	})
	if err != nil && parseError(err) {
		b.Logger.Warn("Telegram could not parse an edit's HTML; sending it as plain text", "error", err)
		params.ParseMode = ""
		params.Text = plainText(text)
		err = b.retryTelegram(ctx, func() error {
			_, err := b.API.EditMessageText(ctx, params)
			return err
		})
	}
	if err != nil && notModified(err) {
		return nil
	}
	// The message stays in the old chat, where it can no longer be edited;
	// what the bot remembers moves to the new one.
	b.followMigration(chatID, err)
	return err
}

// parseError reports whether Telegram refused a message's formatting.
func parseError(err error) bool {
	return errors.Is(err, tgbot.ErrorBadRequest) && strings.Contains(strings.ToLower(err.Error()), "can't parse entities")
}

// notModified reports whether Telegram refused an edit that changes nothing.
func notModified(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "message is not modified")
}

// retryTelegram repeats a Telegram request while Telegram answers 429 Too
// Many Requests, waiting as long as it asks, and a few times, a second apart
// and then longer, after a failure that a moment may cure
// (transientFailure).
func (b *Bot) retryTelegram(ctx context.Context, request func() error) error {
	delay := b.retryDelay
	if delay <= 0 {
		delay = time.Second
	}
	for attempt := 1; ; attempt++ {
		err := request()
		if err == nil || attempt == maxTelegramAttempts {
			return err
		}
		var wait time.Duration
		var limited *tgbot.TooManyRequestsError
		switch {
		case errors.As(err, &limited):
			if wait = time.Duration(limited.RetryAfter) * time.Second; wait > maxRetryAfter {
				return err
			}
			b.Logger.Warn("Telegram rate limit; retrying", "retry_in", wait)
		case transientFailure(err):
			wait = delay << (attempt - 1)
			b.Logger.Warn("Telegram request failed; retrying", "error", err, "retry_in", wait)
		default:
			return err
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return err
		case <-timer.C:
		}
	}
}

// telegramServerError matches the library's error for an answer with a 5xx
// status, which Telegram gives while it is overloaded or restarting.
var telegramServerError = regexp.MustCompile(`error response from telegram for method \w+, 5\d\d\b`)

// transientFailure reports whether a failed Telegram request is worth trying
// again soon: Telegram answered with a server error, or with a page that is
// not its JSON, as a proxy in front of it does, or the connection failed. A
// timeout is not, since Telegram may have done the request by then.
func transientFailure(err error) bool {
	var netErr net.Error
	if errors.Is(err, context.Canceled) || errors.As(err, &netErr) && netErr.Timeout() {
		return false
	}
	text := err.Error()
	if strings.Contains(text, "error decode response body") {
		// A page that is not Telegram's JSON; not Telegram's own answer
		// that the library could not decode.
		return !strings.Contains(text, `"ok":false`)
	}
	var urlErr *url.Error
	return telegramServerError.MatchString(text) || errors.As(err, &urlErr)
}
