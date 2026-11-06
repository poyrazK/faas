package pgproxy

import (
	"context"
	"io"
	"sync"
)

// interruptSetup makes blocking protocol I/O obey setup's context even when
// the stream itself must outlive it. Finish detaches only after cancellation's
// close has completed, so a timed-out stream can never enter the idle pool.
func interruptSetup(ctx context.Context, stream io.Closer) func() error {
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = stream.Close()
		close(done)
	})
	var once sync.Once
	var err error
	return func() error {
		once.Do(func() {
			if stop() {
				close(done)
			}
			<-done
			err = ctx.Err()
		})
		return err
	}
}
