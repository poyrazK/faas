package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"time"
)

// Legacy single-endpoint verification retains its maintenance refusal. Normal
// deployments use finishVerifiedMCPDeploy with an explicit unpromoted candidate.
func finishMCPDeploy(ctx context.Context, c *Client, slug string, cfg mcphosting.Config, token string) int {
	app, err := c.GetApp(ctx, slug)
	if err != nil {
		return printErr("MCP app lookup", err)
	}
	endpoint, err := cfg.URL(canonicalAppURL(app))
	if err != nil {
		return printErr("MCP endpoint", err)
	}
	if cfg.Auth.Mode == "external-oauth" && cfg.Auth.Resource != endpoint {
		return printErr("MCP audience mismatch", fmt.Errorf("auth.resource must match the app's canonical endpoint %s", endpoint))
	}
	if app.MaintenanceMode {
		return printErr("MCP app in maintenance", errors.New("review the failure and clear maintenance with `gregale app <slug> --no-maintenance` before activating a deployment"))
	}
	enabled, require := true, false
	_, err = c.UpdateApp(ctx, slug, api.UpdateAppRequest{StreamingEnabled: &enabled, RequireAuthn: &require, PublicAuth: &api.PublicAuthBlock{Mode: api.AppPublicAuthModeOpen}})
	if err != nil {
		return printErr("MCP ingress configuration", err)
	}
	probe, err := mcphosting.NewClient(endpoint, token, mcphosting.ProtocolVersion)
	if err != nil {
		return printErr("MCP endpoint", err)
	}
	if cfg.Auth.Mode == "external-oauth" {
		probe.ExpectedAuth = &cfg.Auth
	}
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	report := mcphosting.Doctor(probeCtx, probe, cfg.Legacy, "", nil)
	cancel()
	receipt := map[string]any{"app": slug, "endpoint": endpoint, "auth_mode": cfg.Auth.Mode, "verification": report, "connection": mcphosting.ConnectionConfig(slug, endpoint)}
	if !report.OK {
		maintenance := true
		_, closeErr := c.UpdateApp(ctx, slug, api.UpdateAppRequest{MaintenanceMode: &maintenance})
		if closeErr == nil {
			receipt["verification_failure_action"] = "maintenance_enabled"
		} else {
			receipt["verification_failure_action"] = "maintenance_unconfirmed"
			receipt["recovery_command"] = "gregale app " + slug + " --maintenance"
		}
	}
	if code := jsonOut(writeJSON(receipt)); code != 0 {
		return code
	}
	if !report.OK {
		return 1
	}
	return 0
}
