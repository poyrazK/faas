package toolregistry

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

// taskBindingProbeContext inherits the request deadline and values. Executor
// shutdown is an additional cancellation signal, never a replacement parent.
func taskBindingProbeContext(ctx context.Context, shutdown <-chan struct{}) (context.Context, context.CancelFunc) {
	probe, cancel := context.WithTimeout(ctx, api.HostedTaskBindingProbeTimeout)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-shutdown:
			cancel()
		case <-probe.Done():
		}
	}()
	return probe, func() {
		cancel()
		// Join before the caller releases its bounded private worker slot.
		<-stopped
	}
}
