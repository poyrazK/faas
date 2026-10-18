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
	Name          string                   `json:"name"`
	TokenEnv      string                   `json:"token_env,omitempty"`
	Baseline      string                   `json:"baseline"`
	Authorization *mcpReleaseAuthorization `json:"authorization,omitempty"`
	contract      mcphosting.Contract
	token         string
}

type mcpCatalogPinReceipt struct {
	DiscoveryDeploymentID string `json:"discovery_deployment_id"`
	SHA256                string `json:"sha256"`
}

func validateMCPDeploymentCatalogPin(pin *mcpCatalogPinReceipt, health mcpCatalogHealthReport) error {
	if pin == nil {
		return nil
	}
	if !health.Supported {
		return errors.New("pinned candidate no longer exposes catalog integrity")
	}
	if health.CatalogSHA256 != pin.SHA256 {
		return fmt.Errorf("pinned candidate catalog digest %q differs from discovered digest %q", health.CatalogSHA256, pin.SHA256)
	}
	if !health.Healthy {
		if health.Error != "" {
			return fmt.Errorf("pinned candidate catalog is unhealthy: %s", health.Error)
		}
		return errors.New("pinned candidate catalog is unhealthy")
	}
	return nil
}

func validateMCPDeploymentCatalogFleetPin(pin *mcpCatalogPinReceipt, fleet mcpCatalogFleetHealthReport) error {
	if pin == nil {
		return nil
	}
	if !fleet.Supported {
		if fleet.Error != "" {
			return fmt.Errorf("pinned candidate catalog health is unavailable: %s", fleet.Error)
		}
		return errors.New("pinned candidate no longer exposes catalog integrity")
	}
	if !fleet.Healthy {
		if fleet.Error != "" {
			return fmt.Errorf("pinned candidate replica catalog check failed: %s", fleet.Error)
		}
		return errors.New("pinned candidate replica catalog check is incomplete")
	}
	for _, replica := range fleet.Replicas {
		if err := validateMCPDeploymentCatalogPin(pin, replica); err != nil {
			return fmt.Errorf("replica %s: %w", replica.InstanceID, err)
		}
	}
	return nil
}

func loadMCPReleasePolicy(path string) ([]mcpReleaseRole, error) {
	return loadMCPReleasePolicyWithTokens(path, true)
}

func loadMCPReleasePolicyWithTokens(path string, withTokens bool) ([]mcpReleaseRole, error) {
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
		if withTokens {
			r.token, err = mcpToken(r.TokenEnv)
			if err != nil {
				return nil, fmt.Errorf("role %s: %w", r.Name, err)
			}
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
	for i := range policy.Roles {
		validate := validateMCPAuthorizationDeclarations
		if withTokens {
			validate = loadMCPRoleAuthorization
		}
		if err := validate(&policy.Roles[i]); err != nil {
			return nil, fmt.Errorf("role %s authorization: %w", policy.Roles[i].Name, err)
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

func mcpCandidatePreview(ctx context.Context, c *Client, id string, cfg mcphosting.Config) (string, error) {
	preview, err := c.GetDeploymentURL(ctx, id)
	if err != nil {
		return "", err
	}
	if !preview.Alive || preview.URL == "" {
		return "", errors.New("MCP candidate requires an available deployment preview URL")
	}
	return cfg.URL(preview.URL)
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
	candidate, err := mcpCandidatePreview(ctx, c, id, cfg)
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
		entry := map[string]any{"role": role.Name, "diff": diff}
		if role.Authorization != nil {
			authorization := probe.CheckAuthorization(ctx, role.Authorization.AuthorizationExpectations, catalog, role.Authorization.insufficientToken)
			entry["authorization"] = authorization
			checks = append(checks, entry)
			if !mcpAuthorizationChecksPassed(authorization) {
				return checks, fmt.Errorf("role %s authorization expectations failed; promotion blocked", role.Name)
			}
		} else {
			checks = append(checks, entry)
		}
		if !diff.Compatible {
			return checks, fmt.Errorf("role %s catalog requires review; capture and review its baseline before promoting", role.Name)
		}
	}
	return checks, nil
}

func finishMCPDeployWithSmoke(ctx context.Context, c *Client, slug, id string, cfg mcphosting.Config, token string, roles []mcpReleaseRole, audit *mcpAccessAuditOptions, smoke *mcpSmokeGate, pin ...*mcpCatalogPinReceipt) int {
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
	catalogHealth := probeMCPCatalogDeployment(probeCtx, c, slug, id, candidate, 2*time.Second)
	protocolRoles := roles
	if len(protocolRoles) == 0 {
		protocolRoles = []mcpReleaseRole{{Name: "default", token: token}}
	}
	protocolQualification := qualifyMCPProtocolDeployment(probeCtx, c, slug, id, candidate, cfg.Endpoint, protocolRoles, 2*time.Second)
	catalogIntegrity := map[string]any{"deployment_id": id, "health": catalogHealth}
	protocolIntegrity := map[string]any{"deployment_id": id, "endpoint": cfg.Endpoint, "qualification": protocolQualification}
	var pinErr error
	if len(pin) > 0 && pin[0] != nil {
		catalogIntegrity["pin"] = pin[0]
		pinErr = validateMCPDeploymentCatalogFleetPin(pin[0], catalogHealth)
	}
	receipt := map[string]any{
		"app": slug, "deployment_id": id, "endpoint": endpoint, "candidate_endpoint": candidate,
		"auth_mode": cfg.Auth.Mode, "verification": report, "promoted": false,
		"connection":         mcphosting.ConnectionConfig(slug, endpoint),
		"catalog_integrity":  catalogIntegrity,
		"protocol_integrity": protocolIntegrity,
	}
	if pinErr != nil {
		receipt["promotion_error"] = pinErr.Error()
	}
	if !protocolQualification.Qualified {
		protocolErr := protocolQualification.Error
		if protocolErr == "" {
			protocolErr = "one or more candidate replicas failed exact-instance MCP protocol qualification"
		}
		receipt["promotion_error"] = protocolErr
	}
	var readiness []map[string]any
	ready := true
	for _, role := range protocolRoles {
		version := mcphosting.ProtocolVersion
		var baseline *mcphosting.Contract
		if role.contract.Version != 0 {
			baseline = &role.contract
			version = role.contract.ProtocolVersion
		}
		publicProbe, probeErr := mcphosting.NewClient(candidate, role.token, version)
		if probeErr != nil {
			ready = false
			continue
		}
		result := mcphosting.CheckReadiness(probeCtx, publicProbe, baseline)
		readiness = append(readiness, map[string]any{"role": role.Name, "deployment_id": id, "report": result})
		ready = ready && result.OK
	}
	receipt["readiness"] = readiness
	if !ready {
		receipt["promotion_error"] = "Public MCP readiness failed; candidate remains unpromoted"
	}
	if report.OK && pinErr == nil && protocolQualification.Qualified && ready {
		deployment, policyErr := c.GetDeployment(ctx, id)
		var policy api.MCPReadinessPolicy
		if policyErr == nil {
			if deployment.Scope == "" || deployment.Scope == api.DefaultEnvScope {
				policy, policyErr = c.GetMCPReadinessPolicy(ctx, slug)
			} else {
				policy, policyErr = c.GetMCPReadinessPolicy(ctx, slug, deployment.Scope)
			}
		}
		if policyErr != nil {
			ready = false
			receipt["promotion_error"] = "Platform readiness policy unavailable"
		} else if policy.Enabled {
			stored := []api.MCPReadinessEvidence{}
			for _, required := range policy.Roles {
				matched := false
				for _, role := range protocolRoles {
					if role.Name != required.Name {
						continue
					}
					matched = true
					token := role.token
					if required.Monitor.ManagedOAuth() {
						token = ""
					}
					evidence, e := c.ProbeMCPReadiness(ctx, id, required.Name, token)
					if e != nil || !evidence.Passed || evidence.DeploymentID != id || evidence.Role != required.Name || evidence.PolicyRevision != policy.Revision {
						ready = false
						break
					}
					stored = append(stored, evidence)
				}
				if !matched {
					ready = false
				}
				if !ready {
					break
				}
			}
			receipt["platform_readiness"] = stored
			if !ready {
				receipt["promotion_error"] = "Fresh platform readiness evidence required for every configured role"
			}
		}
	}
	if report.OK && pinErr == nil && protocolQualification.Qualified && ready {
		if catalogHealth.Supported && !catalogHealth.Healthy {
			receipt["promotion_error"] = fmt.Sprintf("catalog health check failed: %s", catalogHealth.Error)
		} else {
			checks, gateErr := verifyMCPRoles(probeCtx, candidate, roles)
			receipt["catalog_checks"] = checks
			if gateErr != nil {
				receipt["promotion_error"] = gateErr.Error()
			} else {
				var policy *api.MCPAccessPolicyExpectation
				if audit != nil {
					access := collectMCPAccessAudit(probeCtx, c, slug, cfg, roles, *audit)
					receipt["access_audit"] = access
					policy = access.PolicyExpectation
					if !access.OK {
						receipt["promotion_error"] = "reviewed MCP access policy drift or missing evidence blocks promotion"
						if code := jsonOut(writeJSON(receipt)); code != 0 {
							return code
						}
						return 1
					}
				}
				if smoke != nil {
					smokeCtx, smokeCancel := context.WithTimeout(ctx, 30*time.Second)
					smokeReport := runMCPSmokeGate(smokeCtx, candidate, token, smoke)
					smokeCancel()
					receipt["smoke_tests"] = smokeReport
					if !smokeReport.OK {
						receipt["promotion_error"] = "MCP smoke fixtures failed; candidate remains unpromoted"
						if code := jsonOut(writeJSON(receipt)); code != 0 {
							return code
						}
						return 1
					}
				}
				_, err = c.PatchDeploymentTrafficIfServingWithMCPAccess(ctx, id, 100, serving, policy)
				if err != nil {
					receipt["promotion_error"] = err.Error()
				} else {
					receipt["promoted"] = true
				}
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
