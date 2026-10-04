package devbridge

import (
	"context"
	"errors"
	"net"
	"time"
)

// RunReconnecting reattaches the same leased session after transport failures.
// Expiry and authorization failures are terminal. Individual HTTP requests
// are never retried: a interrupted stream may have executed a side effect.
func RunReconnecting(ctx context.Context, dial func(context.Context) (net.Conn, error), serve func(context.Context, net.Conn) error, state func(bool)) error {
	delay := 250 * time.Millisecond
	disconnected := false
	for ctx.Err() == nil {
		socket, err := dial(ctx)
		if err == nil {
			if state != nil {
				state(true)
			}
			disconnected = false
			started := time.Now()
			err = serve(ctx, socket)
			_ = socket.Close()
			if time.Since(started) > 10*time.Second {
				delay = 250 * time.Millisecond
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, ErrUnauthorized) {
			return ErrUnauthorized
		}
		if !disconnected && state != nil {
			state(false)
		}
		disconnected = true
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if delay < 5*time.Second {
			delay *= 2
			if delay > 5*time.Second {
				delay = 5 * time.Second
			}
		}
	}
	return ctx.Err()
}
