package sched

import (
	"context"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

func (c *VMMClient) PromoteAdmittedRuntime(ctx context.Context, req *vmmdpb.PromoteAdmittedRuntimeRequest) (runtimeadmission.Receipt, error) {
	p, err := runtimeadmission.PromotionFromProto(req)
	if err != nil {
		return runtimeadmission.Receipt{}, err
	}
	if err := p.Validate(time.Now()); err != nil {
		return runtimeadmission.Receipt{}, err
	}
	resp, err := c.cli.PromoteAdmittedRuntime(ctx, p.ToProto())
	if err != nil {
		return runtimeadmission.Receipt{}, liftErr(err)
	}
	r, decodeErr := runtimeadmission.ReceiptFromProto(resp.GetReceipt())
	if runtimeadmission.RejectUnknown(resp) != nil || decodeErr != nil || p.CheckReceipt(r, time.Now()) != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ApplicationStandardRuntimeCleanupTimeout)
		defer cancel()
		_ = c.Destroy(cleanupCtx, p.Binding.InstanceID)
		return runtimeadmission.Receipt{}, runtimeadmission.ErrInvalid
	}
	return r, nil
}

func (r *VMMRouter) PromoteAdmittedRuntime(ctx context.Context, nodeID string, req *vmmdpb.PromoteAdmittedRuntimeRequest) (runtimeadmission.Receipt, error) {
	if req.GetBinding().GetNodeId() != nodeID {
		return runtimeadmission.Receipt{}, runtimeadmission.ErrStale
	}
	cli, err := r.resolveFor(ctx, nodeID)
	if err != nil {
		return runtimeadmission.Receipt{}, err
	}
	admitted, ok := cli.(interface {
		PromoteAdmittedRuntime(context.Context, *vmmdpb.PromoteAdmittedRuntimeRequest) (runtimeadmission.Receipt, error)
	})
	if !ok {
		return runtimeadmission.Receipt{}, runtimeadmission.ErrUnavailable
	}
	return admitted.PromoteAdmittedRuntime(ctx, req)
}
