// adr:683
package sched

import (
	"context"
	"errors"
	"fmt"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
)

func (c *VMMClient) requireImageHealthcheckSupport(ctx context.Context, app AppSpec) error {
	if !app.ImageHealthcheckRequired {
		return nil
	}
	response, err := c.cli.Ping(ctx, &vmmdpb.PingRequest{})
	if err != nil {
		return fmt.Errorf("sched: verify image healthcheck support: %w", liftErr(err))
	}
	if !response.GetSupportsImageHealthcheck() || !response.GetSupportsImageHealthcheckMonitoring() {
		return errors.New("sched: vmmd does not enforce image healthcheck readiness and runtime monitoring")
	}
	return nil
}

func (c *VMMClient) confirmImageHealthcheck(ctx context.Context, app AppSpec, instance string, response *vmmdpb.WakeResponse, paused bool) error {
	if !app.ImageHealthcheckRequired || response.GetSupportsImageHealthcheck() && response.GetSupportsImageHealthcheckMonitoring() && (paused || response.GetImageHealthcheckVerified()) {
		return nil
	}
	return c.rejectUnverifiedImageHealthcheck(ctx, instance)
}

func (c *VMMClient) rejectUnverifiedImageHealthcheck(ctx context.Context, instance string) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.VMMImageHealthcheckCleanupTimeout)
	defer cancel()
	_, err := c.cli.Destroy(cleanupCtx, &vmmdpb.DestroyRequest{Instance: instance})
	return errors.Join(errors.New("sched: vmmd did not acknowledge a fresh image healthcheck"), err)
}

func (c *VMMClient) ResumeWarmInstanceWithImageHealthcheck(ctx context.Context, instance string) error {
	if err := c.requireImageHealthcheckSupport(ctx, AppSpec{ImageHealthcheckRequired: true}); err != nil {
		return err
	}
	response, err := c.cli.ResumeWarmInstance(ctx, &vmmdpb.ResumeWarmInstanceRequest{Instance: instance, ImageHealthcheckRequired: true})
	if err != nil {
		return liftErr(err)
	}
	if !response.GetImageHealthcheckVerified() || !response.GetSupportsImageHealthcheckMonitoring() {
		return c.rejectUnverifiedImageHealthcheck(ctx, instance)
	}
	return nil
}
