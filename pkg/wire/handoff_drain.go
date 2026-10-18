package wire

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// OldGenerationDrain returns the context a hand-off's old generation
// finishes its requests in (docs/adr/20261018074719-zero-downtime-daemon-restarts.md).
// It ends api.HandoffDrainMaxSeconds from now. endStreams runs one grace
// before that, so SSE and WebSocket clients reconnect to the successor
// before the bound cuts them; when stopping ends (the unit is stopping, or a
// newer hand-off replaces this generation) endStreams runs at once and the
// context ends one grace later. endStreams runs at most once.
func OldGenerationDrain(stopping context.Context, grace time.Duration, endStreams func(), log *slog.Logger) (context.Context, context.CancelFunc) {
	maxDrain := time.Duration(api.HandoffDrainMaxSeconds) * time.Second
	ctx, cancel := context.WithTimeout(context.WithoutCancel(stopping), maxDrain)
	var once sync.Once
	end := func() { once.Do(endStreams) }
	timer := time.AfterFunc(maxDrain-grace, end)
	go func() {
		defer timer.Stop()
		select {
		case <-stopping.Done():
			if log != nil {
				log.Info("old generation asked to stop; ending its streams")
			}
			end()
			time.AfterFunc(grace, cancel)
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

// WatchHandoffs starts a successor on each SIGUSR2 until one serves, then
// calls handedOff. A failed hand-off leaves this generation serving.
func WatchHandoffs(ctx context.Context, daemon string, log *slog.Logger, handedOff func()) {
	requests := HandoffRequests(ctx)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-requests:
				if _, err := Handoff(ctx, HandoffOptions{Log: log}); err != nil {
					log.Warn(daemon+": hand-off failed; this generation keeps serving", "err", err)
					continue
				}
				handedOff()
				return
			}
		}
	}()
}

// The STATUS= texts a hand-off reports to systemd.
const (
	HandoffStatusStarted = "hand-off started"
	HandoffStatusFailed  = "hand-off failed at"
)
