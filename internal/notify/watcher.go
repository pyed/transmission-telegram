package notify

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/pyed/transmission"
)

type TorrentsProvider func(ctx context.Context) (transmission.Torrents, error)
type ChatIDProvider func() int64
type SendFunc func(text string, chatID int64, markdown bool) int

const outageFailureThreshold = 3

const (
	outageMessage   = "⚠️ Transmission is unreachable. I’ll keep retrying."
	recoveryMessage = "✅ Transmission is reachable again."
)

type watcherState struct {
	knownIncomplete     map[int]bool
	initialized         bool
	consecutiveFailures int
	outageNotified      bool
}

func newWatcherState() *watcherState {
	return &watcherState{knownIncomplete: make(map[int]bool)}
}

// StartWatcher monitors Transmission torrent completion via periodic RPC polling.
// This enables completion notifications without requiring access to a local log file.
func StartWatcher(ctx context.Context, interval time.Duration, getTorrents TorrentsProvider, getChatID ChatIDProvider, send SendFunc, logger *slog.Logger) {
	if interval <= 0 {
		interval = 10 * time.Second
	}

	go func() {
		state := newWatcherState()

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				state.poll(ctx, getTorrents, getChatID, send, logger)
			}
		}
	}()
}

func (s *watcherState) poll(ctx context.Context, getTorrents TorrentsProvider, getChatID ChatIDProvider, send SendFunc, logger *slog.Logger) {
	torrents, err := getTorrents(ctx)
	if err != nil {
		s.consecutiveFailures++
		if logger != nil && s.consecutiveFailures == 1 {
			logger.Warn("Notification watcher lost contact with Transmission", "error", err)
		}
		if s.consecutiveFailures >= outageFailureThreshold && !s.outageNotified {
			if cid := getChatID(); cid != 0 {
				send(outageMessage, cid, false)
				s.outageNotified = true
			}
		}
		return
	}

	if s.consecutiveFailures >= outageFailureThreshold {
		if logger != nil {
			logger.Info("Notification watcher reconnected to Transmission")
		}
		if s.outageNotified {
			if cid := getChatID(); cid != 0 {
				send(recoveryMessage, cid, false)
			}
		}
	}
	s.consecutiveFailures = 0
	s.outageNotified = false

	cid := getChatID()
	currentIDs := make(map[int]struct{}, len(torrents))

	for _, t := range torrents {
		currentIDs[t.ID] = struct{}{}
		isComplete := t.PercentDone >= 1.0 || t.Status == transmission.StatusSeeding

		if !s.initialized {
			if !isComplete {
				s.knownIncomplete[t.ID] = true
			}
			continue
		}

		if s.knownIncomplete[t.ID] && isComplete {
			delete(s.knownIncomplete, t.ID)
			if cid != 0 {
				send(fmt.Sprintf("Completed: %s", t.Name), cid, false)
			}
		} else if !isComplete {
			s.knownIncomplete[t.ID] = true
		}
	}

	for id := range s.knownIncomplete {
		if _, exists := currentIDs[id]; !exists {
			delete(s.knownIncomplete, id)
		}
	}

	s.initialized = true
}
