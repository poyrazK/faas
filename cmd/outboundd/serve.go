package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/outbound"
	"github.com/onebox-faas/faas/pkg/wire"
)

// drainServer is one of outboundd's HTTP servers with its bound listener.
type drainServer struct {
	name     string
	server   *http.Server
	listener net.Listener
}

// aiCallDrainer ends and waits out a generation's billable AI calls.
type aiCallDrainer interface {
	DrainAICalls(context.Context)
	EndAICalls()
}

// cacheSnapshotter hands the caches to the next generation
// (ADR-20261128150000).
type cacheSnapshotter interface {
	WriteCacheSnapshot(dir string, now time.Time) (int, error)
	DropCaches()
}

// watchHandoffs starts a successor on each SIGUSR2 until one serves, then
// drops this generation's caches, calls handedOff and closes the returned
// channel. The caches are written once the hand-off's memory check has
// passed, so their tmpfs file does not use up the headroom it measures, and
// before the successor starts, so it loads them before it serves. A failed
// hand-off deletes them and this generation keeps serving.
func watchHandoffs(ctx context.Context, caches cacheSnapshotter, cacheDir string, log *slog.Logger, handedOff func()) <-chan struct{} {
	done := make(chan struct{})
	requests := wire.HandoffRequests(ctx)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-requests:
			}
			prepare := func() {
				if n, err := caches.WriteCacheSnapshot(cacheDir, time.Now()); err != nil {
					log.Warn("outboundd: cache snapshot for the successor failed; it starts with empty caches", "err", err)
				} else if n > 0 {
					log.Info("outboundd: wrote caches for the successor", "responses", n)
				}
			}
			if _, err := wire.Handoff(ctx, wire.HandoffOptions{Log: log, Prepare: prepare}); err != nil {
				outbound.RemoveCacheSnapshot(cacheDir)
				log.Warn("outboundd: hand-off failed; this generation keeps serving", "err", err)
				continue
			}
			// The successor holds the same entries; keeping a second copy
			// through a drain of up to 30 minutes would double the unit's
			// cache memory and could refuse the next hand-off.
			caches.DropCaches()
			close(done)
			handedOff()
			return
		}
	}()
	return done
}

// serveGeneration serves until generation ends: the unit stopping, or a
// successor taking over (handoff closed). A stopping generation drains for
// outbounddDrainGrace, ends the AI calls still running so they settle, and
// writes its caches for the next start. A handed-off one lets its requests
// and AI calls run for up to api.HandoffDrainMaxSeconds (ADR-1736); a stop
// during that ends its AI calls at once and allows one grace.
func serveGeneration(stopping, generation context.Context, handoff <-chan struct{}, servers []*drainServer, calls aiCallDrainer,
	caches cacheSnapshotter, cacheDir string, log *slog.Logger) error {
	drains := make([]*wire.HTTPDrain, len(servers))
	served := make(chan error, len(servers))
	for i, s := range servers {
		drains[i] = wire.NewHTTPDrain(s.server)
		go func() {
			err := drains[i].ServeErr(s.server.Serve(drains[i].Listener(s.listener)))
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				served <- errors.Join(errors.New("outboundd: "+s.name+" server"), err)
				return
			}
			served <- nil
		}()
	}
	var serveErr error
	select {
	case serveErr = <-served:
	case <-generation.Done():
	}
	handedOff := false
	select {
	case <-handoff:
		handedOff = true
	default:
	}
	var drainCtx context.Context
	var cancel context.CancelFunc
	if handedOff {
		drainCtx, cancel = wire.OldGenerationDrain(stopping, outbounddDrainGrace, calls.EndAICalls, log)
	} else {
		drainCtx, cancel = context.WithTimeout(context.WithoutCancel(stopping), outbounddDrainGrace)
	}
	defer cancel()
	var wg sync.WaitGroup
	for i, s := range servers {
		wg.Go(func() { _ = drains[i].Shutdown(drainCtx, s.server) })
	}
	// Billable AI calls still running near the end of the drain are ended so
	// they settle at their reservation before the process exits.
	wg.Go(func() { calls.DrainAICalls(drainCtx) })
	wg.Wait()
	if !handedOff && caches != nil {
		if n, err := caches.WriteCacheSnapshot(cacheDir, time.Now()); err != nil {
			log.Warn("outboundd: cache snapshot at stop failed; the next start has empty caches", "err", err)
		} else if n > 0 {
			log.Info("outboundd: wrote caches for the next start", "responses", n)
		}
	}
	return serveErr
}
