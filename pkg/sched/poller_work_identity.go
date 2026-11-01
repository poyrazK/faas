package sched

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// workIdentityPoller translates durable record identities back to the
// current broker delivery handles. A redelivery may have a new handle, but
// it must still match the same trigger_records receipt.
type workIdentityPoller struct {
	triggerSource
	handles map[string][]string
}

func (p *workIdentityPoller) deliveryHandles(ids []string) []string {
	var handles []string
	for _, id := range ids {
		handles = append(handles, p.handles[id]...)
	}
	return handles
}

func (p *workIdentityPoller) Ack(ctx context.Context, t sqlc.Trigger, ids []string) error {
	return p.triggerSource.Ack(ctx, t, p.deliveryHandles(ids))
}

func (p *workIdentityPoller) Nack(ctx context.Context, t sqlc.Trigger, ids []string, reason string) error {
	return p.triggerSource.Nack(ctx, t, p.deliveryHandles(ids), reason)
}

// Defer and NackUntil keep the wrapped poller's batch carry-over and retry
// hold for work-bound triggers (ADR-20261101093512).
func (p *workIdentityPoller) Defer(ctx context.Context, t sqlc.Trigger, ids []string) error {
	if deferrer, ok := p.triggerSource.(triggerDeferrer); ok {
		return deferrer.Defer(ctx, t, p.deliveryHandles(ids))
	}
	return p.triggerSource.Nack(ctx, t, p.deliveryHandles(ids), triggerReasonBrokerError)
}

func (p *workIdentityPoller) NackUntil(ctx context.Context, t sqlc.Trigger, ids []string, until []time.Time) error {
	scheduler, ok := p.triggerSource.(triggerRetryScheduler)
	if !ok {
		return p.triggerSource.Nack(ctx, t, p.deliveryHandles(ids), triggerReasonBrokerError)
	}
	var handles []string
	var at []time.Time
	for i, id := range ids {
		for _, handle := range p.handles[id] {
			handles, at = append(handles, handle), append(at, until[i])
		}
	}
	return scheduler.NackUntil(ctx, t, handles, at)
}
