package main

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func checkMCPConditionalParking(ctx context.Context, c *Client) mcphosting.Check {
	check := mcphosting.Check{Name: "conditional_parking", Status: "unknown"}
	capabilities, err := c.GetCapabilities(ctx)
	if err != nil {
		check.Detail = "Could not read control-plane capabilities. Check authentication, read scope, and connectivity; upgrade the control plane if capability discovery is unsupported."
		return check
	}
	if !capabilities.ConditionalParking {
		check.Status = "failed"
		check.Detail = "Conditional parking is unsupported. Upgrade all control-plane instances to a version with conditional parking and a supported state backend before retrying."
		return check
	}
	check.Status = "passed"
	check.Detail = "The serving control plane advertises atomic deployment-guarded parking; guarded requests remain required in mixed-version fleets"
	return check
}

func requireMCPConditionalParking(ctx context.Context, c *Client) error {
	check := checkMCPConditionalParking(ctx, c)
	if check.Status != "passed" {
		return errors.New(check.Detail)
	}
	return nil
}

func preflightMCPControlPlane() error {
	c, err := authedClient()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return requireMCPConditionalParking(ctx, c)
}
