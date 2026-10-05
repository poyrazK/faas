// adr: 590 — compose native admission and configuration readiness publication.
package sched

import (
	"context"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

func (e *Engine) publishRuntimeWithStandardConfig(ctx context.Context, id, expected string, out *WakeOutcome, wakeID string, inputs *state.RuntimeConfigInputs, appID string) (state.Instance, error) {
	if out == nil {
		return state.Instance{}, runtimeadmission.ErrInvalid
	}
	if out.RuntimeAdmissionReceipt == nil {
		return e.publishRuntimeConfigReceipt(ctx, id, expected, out.Netns, out.HostIP, int(out.LeaseUID), wakeID, inputs, appID)
	}
	if err := e.checkManagedPostgresAdmission(ctx, appID); err != nil {
		return state.Instance{}, err
	}
	receipt := out.RuntimeAdmissionReceipt
	if receipt.Binding.InstanceID != id || receipt.Netns != out.Netns || receipt.HostIP != out.HostIP || receipt.LeaseUID != out.LeaseUID {
		return state.Instance{}, runtimeadmission.ErrInvalid
	}
	if inputs == nil {
		return e.publishRuntimeWithStandards(ctx, id, expected, state.StateRunning, out)
	}
	publisher, ok := e.store.(state.InstanceApplicationStandardConfigPublisher)
	if !ok {
		return state.Instance{}, runtimeadmission.ErrUnavailable
	}
	return publisher.PublishInstanceApplicationStandardRuntimeWithConfig(ctx, expected, state.StateRunning, *receipt, wakeID, *inputs)
}
