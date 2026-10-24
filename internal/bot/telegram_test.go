package bot

import (
	"context"
	"strings"
	"testing"
)

func TestChunkMessageShortAndEmpty(t *testing.T) {
	if chunks := chunkMessage("Hello <b>world</b>"); len(chunks) != 1 || chunks[0] != "Hello <b>world</b>" {
		t.Fatalf("expected one chunk, got %q", chunks)
	}
	if chunks := chunkMessage(""); len(chunks) != 0 {
		t.Fatalf("expected no chunks for an empty message, got %q", chunks)
	}
}

func TestChunkMessageSplitsBetweenLines(t *testing.T) {
	line1 := strings.Repeat("a", 2000)
	line2 := strings.Repeat("b", 2000)
	line3 := strings.Repeat("c", 2000)

	chunks := chunkMessage(line1 + "\n" + line2 + "\n" + line3)
	if len(chunks) != 2 || chunks[0] != line1+"\n"+line2 || chunks[1] != line3 {
		t.Fatalf("expected the third line in a chunk of its own, got %d chunks", len(chunks))
	}
}

// Telegram counts what it shows: tags and entities are not part of the
// length, so a message of 4,096 visible characters fits whatever its markup.
func TestChunkMessageMeasuresTheTextTelegramShows(t *testing.T) {
	var lines []string
	for range 64 {
		// 64 visible characters each, newline included, in more markup.
		lines = append(lines, "<code>1</code> <b>"+strings.Repeat("&amp;", 61)+"</b>")
	}
	text := strings.Join(lines, "\n")
	if got := renderedLength(text); got != 64*64-1 {
		t.Fatalf("renderedLength = %d, want %d", got, 64*64-1)
	}
	if chunks := chunkMessage(text); len(chunks) != 1 {
		t.Fatalf("a message that fits was split into %d chunks", len(chunks))
	}
}

// Emoji outside the Basic Multilingual Plane count twice in Telegram's
// limit, so 5,000 of them need three messages, not two.
func TestChunkMessageCountsUTF16Units(t *testing.T) {
	chunks := chunkMessage(strings.Repeat("🚀", 5000))
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}
	for i, chunk := range chunks {
		if got := renderedLength(chunk); got > maxMessageLength {
			t.Errorf("chunk %d is %d UTF-16 units long", i, got)
		}
	}
}

// A single line longer than a message is cut as plain text, and whatever it
// cuts stays escaped.
func TestChunkMessageCutsOverlongLines(t *testing.T) {
	line := "<b>" + strings.Repeat("&lt;", 5000) + "</b>"
	chunks := chunkMessage(line)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	if got := plainText(chunks[0]) + plainText(chunks[1]); got != strings.Repeat("<", 5000) {
		t.Fatalf("the text changed while being cut: %d characters", len(got))
	}
	for i, chunk := range chunks {
		if strings.Contains(chunk, "<") {
			t.Errorf("chunk %d is not escaped: %.40q", i, chunk)
		}
	}
}

func TestPlainTextAndEscaping(t *testing.T) {
	name := `<Movie> & "Show" S01_E02 *1080p* [x264]`
	if got := plainText("<b>" + esc(name) + "</b>"); got != name {
		t.Fatalf("plainText(esc(name)) = %q, want %q", got, name)
	}
}

func TestSendUsesHTMLAndReturnsTheMessageID(t *testing.T) {
	ft := newFakeTelegram(t)
	b := newTestBot(t, ft.newBot(t), nil)

	id, err := b.send(context.Background(), 42, "<b>bold</b> &amp; plain", nil)
	if err != nil || id == 0 {
		t.Fatalf("send = %d, %v", id, err)
	}
	if modes := ft.sentParseModes(); len(modes) != 1 || modes[0] != "HTML" {
		t.Fatalf("parse modes = %q, want [HTML]", modes)
	}
}

// A reply of more than three messages is attached as a file, as plain text,
// instead of flooding the chat.
func TestSendAttachesVeryLongRepliesAsAFile(t *testing.T) {
	ft := newFakeTelegram(t)
	b := newTestBot(t, ft.newBot(t), nil)
	var lines []string
	for i := range 400 {
		lines = append(lines, "<code>1</code> <b>"+esc(strings.Repeat("x", 40))+"</b> &amp; "+strings.Repeat("y", i%7))
	}

	id, err := b.send(context.Background(), 42, strings.Join(lines, "\n"), nil)
	if err != nil || id != 0 {
		t.Fatalf("send = %d, %v; want 0 and no error", id, err)
	}
	if texts := ft.sentTexts(); len(texts) != 0 {
		t.Fatalf("also sent %d messages", len(texts))
	}
	documents := ft.sentDocuments()
	if len(documents) != 1 || strings.Contains(documents[0], "<b>") || !strings.Contains(documents[0], "& ") {
		t.Fatalf("expected one plain-text file, got %d", len(documents))
	}
}

// Telegram's flood limit (429 with retry_after) and its passing failures
// (5xx, pages that are not its JSON) are retried; other refusals are not.
func TestSendRetriesPassingFailures(t *testing.T) {
	ft := newFakeTelegram(t)
	b := newTestBot(t, ft.newBot(t), nil)
	ft.script("sendMessage",
		`{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 0","parameters":{"retry_after":0}}`,
		`{"ok":false,"error_code":502,"description":"Bad Gateway"}`,
		`<html>502 Bad Gateway</html>`,
	)
	if _, err := b.send(context.Background(), 42, "hello", nil); err != nil {
		t.Fatalf("send gave up: %v", err)
	}
	if got := ft.callCount("sendMessage"); got != 4 {
		t.Fatalf("expected three failures and a success, got %d calls", got)
	}

	ft.script("sendMessage", `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`)
	if _, err := b.send(context.Background(), 42, "hello", nil); err == nil {
		t.Fatal("expected the refusal to be returned")
	}
	if got := ft.callCount("sendMessage"); got != 5 {
		t.Fatalf("a refusal was retried: %d calls", got)
	}
}

// HTML Telegram cannot parse, which only a bug in the bot causes, still
// reaches the chat, as the plain text it was meant to show.
func TestSendFallsBackToPlainTextOnParseErrors(t *testing.T) {
	ft := newFakeTelegram(t)
	b := newTestBot(t, ft.newBot(t), nil)
	ft.script("sendMessage", `{"ok":false,"error_code":400,"description":"Bad Request: can't parse entities: unclosed start tag"}`)

	if _, err := b.send(context.Background(), 42, "<b>name &amp; more", nil); err != nil {
		t.Fatalf("send failed: %v", err)
	}
	texts, modes := ft.sentTexts(), ft.sentParseModes()
	if len(texts) != 1 || texts[0] != "name & more" || modes[0] != "" {
		t.Fatalf("expected the plain text without a parse mode, got %q in %q", texts, modes)
	}
}

func TestEditRefusesTextLongerThanAMessage(t *testing.T) {
	ft := newFakeTelegram(t)
	b := newTestBot(t, ft.newBot(t), nil)
	if err := b.edit(context.Background(), 42, 1, strings.Repeat("a", maxMessageLength+1), nil); err != errMessageTooLong {
		t.Fatalf("edit = %v, want errMessageTooLong", err)
	}
	if got := ft.callCount("editMessageText"); got != 0 {
		t.Fatalf("asked Telegram anyway: %d calls", got)
	}
}

func TestEditTreatsAnUnchangedMessageAsDone(t *testing.T) {
	ft := newFakeTelegram(t)
	b := newTestBot(t, ft.newBot(t), nil)
	ft.script("editMessageText", `{"ok":false,"error_code":400,"description":"Bad Request: message is not modified"}`)
	if err := b.edit(context.Background(), 42, 1, "same", nil); err != nil {
		t.Fatalf("edit = %v, want nil", err)
	}
}
