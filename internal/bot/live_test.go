package bot

import (
	"context"
	"testing"
	"time"
)

func TestLiveUpdateSkipsMissingMessage(t *testing.T) {
	ft := newFakeTelegram(t)
	b := newTestBot(t, ft.newBot(t), nil)
	b.Config.Duration = 1
	b.Config.Interval = time.Millisecond
	called := false

	b.liveUpdate(context.Background(), 42, 0, func() string {
		called = true
		return "update"
	}, nil)

	if called || ft.textEdits() != 0 {
		t.Fatalf("live update ran for an unsent message: called=%v edits=%d", called, ft.textEdits())
	}
}

func TestLiveUpdateEditsAndFinalizes(t *testing.T) {
	ft := newFakeTelegram(t)
	b := newTestBot(t, ft.newBot(t), nil)
	b.Config.Duration = 2
	b.Config.Interval = time.Millisecond

	b.liveUpdate(context.Background(), 42, 99, func() string { return "update" }, func() string { return "final" })

	if got := ft.textEdits(); got != 3 {
		t.Fatalf("expected two updates and one final edit, got %d", got)
	}
}
