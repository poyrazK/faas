package sched

// adr: 498
import (
	"bytes"
	"context"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

// RegionalWarmResumeVMM must not fall back to a resume without the issued seat.
type RegionalWarmResumeVMM interface {
	ResumeRegionalWarmInstance(context.Context, string, string, runtimequota.Placement) error
}

func regionalResumeWire(instance string, p runtimequota.Placement) ([]byte, error) {
	if p.Validate() != nil || p.Seat == nil || p.Permit.InstanceID != instance {
		return nil, runtimequota.ErrBinding
	}
	return regionalRuntimeToProto(&p)
}

func (c *VMMClient) ResumeRegionalWarmInstance(ctx context.Context, instance string, p runtimequota.Placement) error {
	raw, err := regionalResumeWire(instance, p)
	if err != nil {
		return err
	}
	response, err := c.cli.ResumeWarmInstance(ctx, &vmmdpb.ResumeWarmInstanceRequest{Instance: instance, RegionalRuntime: raw})
	if err != nil {
		return err
	}
	if response.GetInstance() != instance || !bytes.Equal(response.GetRegionalRuntime(), raw) {
		return runtimequota.ErrTrust
	}
	return nil
}

func (r *VMMRouter) ResumeRegionalWarmInstance(ctx context.Context, node, instance string, p runtimequota.Placement) error {
	if _, err := regionalResumeWire(instance, p); err != nil {
		return err
	}
	if p.Permit.NodeID != node {
		return runtimequota.ErrBinding
	}
	cli, err := r.resolveFor(ctx, node)
	if err != nil {
		return err
	}
	resumer, ok := cli.(interface {
		ResumeRegionalWarmInstance(context.Context, string, runtimequota.Placement) error
	})
	if !ok {
		return api.NewProblem(501, api.CodeNotImplemented,
			"Regional resume unavailable", "vmmd client does not support explicit regional resume")
	}
	return resumer.ResumeRegionalWarmInstance(ctx, instance, p.Clone())
}
