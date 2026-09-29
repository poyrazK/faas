package sched

import (
	"context"

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
