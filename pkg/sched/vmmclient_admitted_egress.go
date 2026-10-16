package sched

import (
	"context"
	"fmt"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

// Missing capability and malformed acknowledgments never fall back to a legacy RPC.
func (c *VMMClient) UpdateAdmittedAppEgressPolicy(ctx context.Context, identity runtimeadmission.Identity, p runtimeadmission.EgressPolicy) (runtimeadmission.EgressReceipt, error) {
	p = p.Clone()
	req, err := runtimeadmission.EgressRequest(identity, p)
	if err != nil {
		return runtimeadmission.EgressReceipt{}, err
	}
	ack, err := c.cli.UpdateAdmittedAppEgressPolicy(ctx, req)
	if err != nil {
		return runtimeadmission.EgressReceipt{}, fmt.Errorf("update admitted app egress policy: %w", err)
	}
	receipt, err := runtimeadmission.EgressReceiptFromProto(ack)
	if err != nil {
		return runtimeadmission.EgressReceipt{}, err
	}
	if err := receipt.Check(identity, p); err != nil {
		return runtimeadmission.EgressReceipt{}, err
	}
	return receipt, nil
}

func (r *VMMRouter) UpdateAdmittedAppEgressPolicy(ctx context.Context, identity runtimeadmission.Identity, p runtimeadmission.EgressPolicy) (runtimeadmission.EgressReceipt, error) {
	client, err := r.resolveFor(ctx, identity.NodeID)
	if err != nil {
		return runtimeadmission.EgressReceipt{}, err
	}
	updater, ok := client.(interface {
		UpdateAdmittedAppEgressPolicy(context.Context, runtimeadmission.Identity, runtimeadmission.EgressPolicy) (runtimeadmission.EgressReceipt, error)
	})
	if !ok {
		return runtimeadmission.EgressReceipt{}, runtimeadmission.ErrUnavailable
	}
	return updater.UpdateAdmittedAppEgressPolicy(ctx, identity, p)
}
