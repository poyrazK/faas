package fcvm

import (
	"context"
	"errors"
	"io"
	"syscall"
	"time"
)

// Snapshot transport reset can close a newly accepted vsock connection before
// the guest consumes its resume request. Retry only transport closure; a NACK
// remains terminal. Every attempt performs the full entropy/clock hook and must
// receive ACK=0 before Restore may expose the VM as ready.
func retryResumeTransport(ctx context.Context, attempt func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, resumeHookDialDeadline)
	defer cancel()
	for n := 0; ; n++ {
		err := attempt(ctx)
		if err == nil || n == 2 || ctx.Err() != nil || !isResumeTransportReset(err) {
			return err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func isResumeTransportReset(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE)
}
