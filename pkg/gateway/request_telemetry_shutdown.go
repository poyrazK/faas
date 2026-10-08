// adr: 570
package gateway

import (
	"context"
	"log/slog"
)

// Start launches one actor. A stopped or disabled publisher cannot restart.
func (p *requestTelemetryPublisher) Start(ctx context.Context) {
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()
	if p.started || p.stopped || !p.cfg.Enabled {
		return
	}
	p.started, p.parentCtx = true, ctx
	ctx, p.cancel = context.WithCancel(ctx)
	go p.run(ctx)
}

// Stop gives evidence one independent bounded final flush and joins the actor.
// Every concurrent caller waits for the same completion. Producers must stop
// before this call. Stop before Start is terminal and performs no work.
func (p *requestTelemetryPublisher) Stop() {
	p.lifecycleMu.Lock()
	ctx := context.Background()
	if p.started {
		ctx = context.WithoutCancel(p.parentCtx)
	}
	p.stopLocked(ctx)
	p.lifecycleMu.Unlock()
	<-p.doneCh
}

// StopWithContext also honors the owner's shared cleanup deadline. This context
// must outlive ordinary daemon cancellation; its cancellation is authoritative.
func (p *requestTelemetryPublisher) StopWithContext(ctx context.Context) {
	p.lifecycleMu.Lock()
	p.stopLocked(ctx)
	p.lifecycleMu.Unlock()
	<-p.doneCh
}

func (p *requestTelemetryPublisher) stopLocked(ctx context.Context) {
	if !p.stopped {
		p.stopped = true
		if p.started {
			p.finalCtx, p.finalCancel = context.WithTimeout(ctx, p.cfg.ShutdownTimeout)
			p.cancel()
		} else {
			close(p.doneCh)
		}
		// Publishing finalCtx precedes the actor's observation of this close.
		close(p.stopCh)
	}
}

func (p *requestTelemetryPublisher) drainFinal() {
	for len(p.pending) != 0 || p.recorder.PendingCount() != 0 {
		if p.finalCtx.Err() != nil {
			p.dropPending("request telemetry shutdown deadline; dropping interrupted batch")
			for p.recorder.PendingCount() != 0 {
				p.recordDropped(requestTelemetryCount(p.recorder.DrainBatch(p.cfg.FlushBatchSize)))
			}
			p.log.Warn("request telemetry shutdown deadline; queued evidence dropped")
			return
		}
		p.tick(p.finalCtx)
	}
}

func (p *requestTelemetryPublisher) dropPending(message string) {
	if len(p.pending) == 0 {
		return
	}
	count := requestTelemetryCount(p.pending)
	p.log.Warn(message, slog.Int("batch_size", len(p.pending)), slog.Int64("request_count", count))
	p.recordDropped(count)
	p.pending = nil
}
