package wire

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"
)

// ServeUntilShutdown serves server on listener until ctx ends, then stops
// server (see HTTPDrain) and every stop function at once and returns only
// after they finished or grace expired.
//
// A run function that starts Shutdown in a goroutine and returns when
// ListenAndServe does exits too early: ListenAndServe returns
// http.ErrServerClosed as soon as Shutdown begins, and wire.Daemon then exits
// the process under the requests still in flight
// (docs/adr/1736-zero-downtime-daemon-restarts.md).
func ServeUntilShutdown(ctx context.Context, server *http.Server, listener net.Listener, grace time.Duration, stops ...func(context.Context)) error {
	drain := NewHTTPDrain(server)
	served := make(chan error, 1)
	go func() { served <- server.Serve(drain.Listener(listener)) }()
	select {
	case err := <-served:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), grace)
	defer cancel()
	var wg sync.WaitGroup
	wg.Go(func() { _ = drain.Shutdown(shutdownCtx, server) })
	for _, stop := range stops {
		wg.Go(func() { stop(shutdownCtx) })
	}
	wg.Wait()
	return nil
}
