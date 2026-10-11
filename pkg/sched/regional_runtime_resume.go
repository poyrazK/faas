package sched

// adr: 752
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
	if p.Validate() != nil || p.Seat == nil || !runtimequota.MatchesID(p.Permit.InstanceID, instance) {
		return nil, runtimequota.ErrBinding
	}
	return regionalRuntimeToProto(&p)
}

func (c *VMMClient) ResumeRegionalWarmInstance(ctx context.Context, instance string, p runtimequota.Placement) error {
	return c.resumeRegionalWarmInstance(ctx, instance, p, false)
}

func (c *VMMClient) ResumeRegionalWarmInstanceWithImageHealthcheck(ctx context.Context, instance string, p runtimequota.Placement) error {
	return c.resumeRegionalWarmInstance(ctx, instance, p, true)
}

func (c *VMMClient) resumeRegionalWarmInstance(ctx context.Context, instance string, p runtimequota.Placement, imageRequired bool) error {
	raw, err := regionalResumeWire(instance, p)
	if err != nil {
		return err
	}
	if imageRequired {
		if err := c.requireImageHealthcheckSupport(ctx, AppSpec{ImageHealthcheckRequired: true}); err != nil {
			return err
		}
	}
	response, err := c.cli.ResumeWarmInstance(ctx, &vmmdpb.ResumeWarmInstanceRequest{Instance: instance, RegionalRuntime: raw, ImageHealthcheckRequired: imageRequired})
	if err != nil {
		return err
	}
	if response.GetInstance() != instance || !bytes.Equal(response.GetRegionalRuntime(), raw) ||
		imageRequired && (!response.GetImageHealthcheckVerified() || !response.GetSupportsImageHealthcheckMonitoring()) {
		return runtimequota.ErrTrust
	}
	return nil
}

func (r *VMMRouter) ResumeRegionalWarmInstance(ctx context.Context, node, instance string, p runtimequota.Placement) error {
	if _, err := regionalResumeWire(instance, p); err != nil {
		return err
	}
	if !runtimequota.MatchesID(p.Permit.NodeID, node) {
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

// Checked regional resumes must never fall back to either an unchecked regional
// resume or an independent image-only resume.
func (r *VMMRouter) ResumeRegionalWarmInstanceWithImageHealthcheck(ctx context.Context, node, instance string, p runtimequota.Placement) error {
	if _, err := regionalResumeWire(instance, p); err != nil {
		return err
	}
	if !runtimequota.MatchesID(p.Permit.NodeID, node) {
		return runtimequota.ErrBinding
	}
	cli, err := r.resolveFor(ctx, node)
	if err != nil {
		return err
	}
	resumer, ok := cli.(interface {
		ResumeRegionalWarmInstanceWithImageHealthcheck(context.Context, string, runtimequota.Placement) error
	})
	if !ok {
		return api.NewProblem(501, api.CodeNotImplemented, "Checked regional resume unavailable", "vmmd client cannot acknowledge both the issued seat and image readiness")
	}
	return resumer.ResumeRegionalWarmInstanceWithImageHealthcheck(ctx, instance, p.Clone())
}
