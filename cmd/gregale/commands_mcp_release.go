package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

type mcpReleaseRole struct {
	Name     string `json:"name"`
	TokenEnv string `json:"token_env,omitempty"`
	Baseline string `json:"baseline"`
	contract mcphosting.Contract
	token    string
}

func loadMCPReleasePolicy(path string) ([]mcpReleaseRole, error) {
	if path == "" {
		return nil, nil
	}
	f, err := openCustomerFile(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var policy struct {
		Version int              `json:"version"`
		Roles   []mcpReleaseRole `json:"roles"`
	}
	body, err := io.ReadAll(io.LimitReader(f, api.MCPReleasePolicyMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > api.MCPReleasePolicyMaxBytes {
		return nil, errors.New("MCP release policy exceeds 64 KiB")
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(&policy); err != nil {
		return nil, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return nil, errors.New("release policy must contain one JSON object")
	}
	if policy.Version != 1 || len(policy.Roles) == 0 || len(policy.Roles) > api.MCPReleaseMaxRoles {
		return nil, errors.New("release policy version 1 requires 1-32 roles")
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	seen := map[string]bool{}
	for i := range policy.Roles {
		r := &policy.Roles[i]
		if r.Name == "" || seen[r.Name] || r.Baseline == "" {
			return nil, errors.New("release roles require unique names and baseline paths")
		}
		seen[r.Name] = true
		r.token, err = mcpToken(r.TokenEnv)
		if err != nil {
			return nil, fmt.Errorf("role %s: %w", r.Name, err)
		}
		baseline, err := root.Open(r.Baseline)
		if err != nil {
			return nil, err
		}
		r.contract, err = mcphosting.ReadContract(baseline)
		err = errors.Join(err, baseline.Close())
		if err != nil {
			return nil, fmt.Errorf("role %s baseline: %w", r.Name, err)
		}
		if r.contract.Version != 2 {
			return nil, fmt.Errorf("role %s requires a full catalog format-2 baseline", r.Name)
		}
	}
	return policy.Roles, nil
}

func mcpServingDeployment(ctx context.Context, c *Client, slug, candidate string) (string, error) {
	deps, err := c.ListAppDeploymentsAll(ctx, slug)
	if err != nil {
		return "", err
	}
	serving := ""
	for _, dep := range deps {
		if dep.ID == candidate || dep.Status != statusLive || dep.TrafficPercent == 0 {
			continue
		}
		if dep.TrafficPercent != 100 || serving != "" {
			return "", errors.New("MCP promotion requires a single serving revision; resolve the traffic split first")
		}
		serving = dep.ID
	}
	return serving, nil
}

func prepareMCPCandidate(ctx context.Context, c *Client, slug, id string, cfg mcphosting.Config) (string, string, string, error) {
	dep, err := c.GetDeployment(ctx, id)
	if err != nil {
		return "", "", "", err
	}
	app, err := c.GetApp(ctx, slug)
	if err != nil {
		return "", "", "", err
	}
	if dep.AppID != app.ID || dep.Status != statusLive || dep.TrafficPercent != 0 {
		return "", "", "", errors.New("MCP candidate must be live with zero production traffic")
	}
	endpoint, err := cfg.URL(canonicalAppURL(app))
	if err != nil {
		return "", "", "", err
	}
	if cfg.Auth.Mode == "external-oauth" && cfg.Auth.Resource != endpoint {
		return "", "", "", fmt.Errorf("auth.resource must match the canonical endpoint %s", endpoint)
	}
	if app.MaintenanceMode {
		return "", "", "", errors.New("clear app maintenance before verifying an MCP candidate")
	}
	serving, err := mcpServingDeployment(ctx, c, slug, id)
	if err != nil {
		return "", "", "", err
	}
	if serving != "" && (app.RequireAuthn || app.PublicAuth.Mode != api.AppPublicAuthModeOpen || !app.StreamingEnabled) {
		return "", "", "", errors.New("configure MCP streaming and client ingress explicitly before replacing an existing serving app")
	}
	if serving == "" {
		enabled, require := true, false
		if _, err = c.UpdateApp(ctx, slug, api.UpdateAppRequest{StreamingEnabled: &enabled, RequireAuthn: &require, PublicAuth: &api.PublicAuthBlock{Mode: api.AppPublicAuthModeOpen}}); err != nil {
			return "", "", "", err
		}
	}
	preview, err := c.GetDeploymentURL(ctx, id)
	if err != nil {
		return "", "", "", err
	}
	if !preview.Alive || preview.URL == "" {
		return "", "", "", errors.New("MCP promotion requires an available deployment preview URL")
	}
	candidate, err := cfg.URL(preview.URL)
	return endpoint, candidate, serving, err
}

func verifyMCPRoles(ctx context.Context, candidate string, roles []mcpReleaseRole) ([]map[string]any, error) {
	checks := make([]map[string]any, 0, len(roles))
	for _, role := range roles {
		probe, err := mcphosting.NewClient(candidate, role.token, role.contract.ProtocolVersion)
		if err != nil {
			return checks, err
		}
		catalog, _, err := probe.DiscoverCatalog(ctx)
		if err != nil {
			return checks, fmt.Errorf("role %s discovery: %w", role.Name, err)
		}
		var contract mcphosting.Contract
		if role.contract.Version == 1 {
			contract, err = mcphosting.NewContract(probe.Version, catalog.Tools)
		} else {
			contract, err = mcphosting.NewCatalogContract(probe.Version, catalog)
		}
		if err != nil {
			return checks, err
		}
		diff, err := mcphosting.CompareContractsWithOptions(role.contract, contract, mcphosting.ContractDiffOptions{StrictCatalog: true})
		if err != nil {
			return checks, err
		}
		checks = append(checks, map[string]any{"role": role.Name, "diff": diff})
		if !diff.Compatible {
			return checks, fmt.Errorf("role %s catalog requires review; capture and review its baseline before promoting", role.Name)
		}
	}
	return checks, nil
}

func finishVerifiedMCPDeploy(ctx context.Context, c *Client, slug, id string, cfg mcphosting.Config, token string, roles []mcpReleaseRole) int {
	endpoint, candidate, serving, err := prepareMCPCandidate(ctx, c, slug, id, cfg)
	if err != nil {
		return printErr("MCP candidate", err)
	}
	probe, err := mcphosting.NewClient(candidate, token, mcphosting.ProtocolVersion)
	if err != nil {
		return printErr("MCP endpoint", err)
	}
	if cfg.Auth.Mode == "external-oauth" {
		probe.ExpectedAuth = &cfg.Auth
	}
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	report := mcphosting.Doctor(probeCtx, probe, cfg.Legacy, "", nil)
	receipt := map[string]any{"app": slug, "deployment_id": id, "endpoint": endpoint, "candidate_endpoint": candidate, "auth_mode": cfg.Auth.Mode, "verification": report, "promoted": false, "connection": mcphosting.ConnectionConfig(slug, endpoint)}
	if report.OK {
		checks, gateErr := verifyMCPRoles(probeCtx, candidate, roles)
		receipt["catalog_checks"] = checks
		if gateErr != nil {
			receipt["promotion_error"] = gateErr.Error()
		} else {
			_, err = c.PatchDeploymentTrafficIfServing(ctx, id, 100, serving)
			if err != nil {
				receipt["promotion_error"] = err.Error()
			} else {
				receipt["promoted"] = true
			}
		}
	}
	if code := jsonOut(writeJSON(receipt)); code != 0 {
		return code
	}
	if receipt["promoted"] != true {
		return 1
	}
	return 0
}
