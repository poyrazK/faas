package mcpmanagement

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"path/filepath"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func (m *management) applyToolDeploy(ctx context.Context, a applyArgs) (any, error) {
	if err := m.toolBuildingAccess(); err != nil {
		return nil, err
	}
	if !m.opts.AllowWrites {
		return nil, invalid("writes_disabled", "publication requires writes enabled")
	}
	m.mu.Lock()
	if p := m.toolPublications[a.PlanID]; p == nil || time.Now().After(p.ExpiresAt) && p.Status != "planned" {
		m.mu.Unlock()
		// A fresh bridge may inspect an attempt, but cannot resume it.
		return m.getDurableToolDeploy(ctx, a.PlanID)
	}
	p, err := m.checkedPublication(ctx, a.PlanID)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	// All attempts, including uncertain ones, are single-shot. Returning the
	// retained receipt must never repeat a behavioral fixture's side effects.
	if p.Status != "planned" {
		out := publicationView(p)
		m.mu.Unlock()
		return out, nil
	}
	project, err := loadToolProject(toolProjectArgs{Path: p.Path, Fixtures: p.Fixtures, AccessMode: p.AccessMode, TokenEnv: p.TokenEnv})
	if err == nil && project.digest != p.Digest {
		err = invalid("source_changed", "source or fixtures changed since review; obtain a new publication plan")
	}
	if err == nil {
		err = m.checkToolBaseline(ctx, p)
	}
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	select {
	case toolPublishSlots <- struct{}{}:
	default:
		m.mu.Unlock()
		return nil, invalid("publication_capacity", "publication workers are busy; retry the same unstarted plan")
	}
	source, err := snapshotToolSource(p.Path, p.Digest)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		if source != "" {
			_ = removeToolSnapshot(source)
		}
		<-toolPublishSlots
		m.mu.Unlock()
		return nil, err
	}
	// Detach the transport only. Lifecycle shutdown/revocation and a bounded
	// worker deadline still cancel the CLI. Native calls recheck credentials.
	workCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), toolPublishTimeout)
	workCtx, finish, err := m.lifecycle.enter(workCtx, false)
	if err != nil {
		cancel()
		_ = removeToolSnapshot(source)
		<-toolPublishSlots
		m.mu.Unlock()
		return nil, err
	}
	workCtx, finishGrant, err := m.grantLifecycle.enter(workCtx, false)
	if err != nil {
		finish()
		cancel()
		_ = removeToolSnapshot(source)
		<-toolPublishSlots
		m.mu.Unlock()
		return nil, err
	}
	begin, err := m.client.BeginToolPublication(ctx, p.ID, p.Intent)
	if err == nil {
		err = validatePublicationRecord(begin.Record, p.AccountID, p.ID, &p.Intent)
	}
	if err != nil || !begin.Created || begin.Record.Status != "running" {
		// Once begin is sent, its response can be lost after commit. Remove the
		// local executable plan; all future applies inspect the native identity.
		delete(m.toolPublications, p.ID)
		if p.Uploaded {
			m.opts.uploads.removeDir(p.Path)
		}
		finishGrant()
		finish()
		cancel()
		_ = removeToolSnapshot(source)
		<-toolPublishSlots
		m.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return durablePublicationView(begin.Record), nil
	}
	if p.Uploaded {
		// The asynchronous worker owns its verified snapshot now. Upload expiry
		// cannot change either candidate build, and unused source is reclaimed.
		m.opts.uploads.removeDir(p.Path)
	}
	p.Status, p.ExpiresAt = "running", time.Now().Add(toolPublishTimeout+releasePlanTTL)
	out := publicationView(p)
	m.mu.Unlock()
	go func() {
		defer func() { _ = removeToolSnapshot(source) }()
		defer func() { <-toolPublishSlots }()
		defer cancel()
		defer finish()
		defer finishGrant()
		m.runToolPublication(workCtx, p, source)
	}()
	return out, nil
}

func (m *management) checkToolBaseline(ctx context.Context, p *toolPublication) error {
	app, err := m.client.GetApp(ctx, p.App)
	if notFound(err) && p.AppID == "" {
		return nil
	}
	if err != nil {
		return err
	}
	if app.ID != p.AppID || app.Slug != p.App {
		return invalid("plan_stale", "app identity changed since publication review")
	}
	health, err := m.scopedHealth(ctx, appArgs{App: p.App})
	if err != nil {
		return err
	}
	serving := ""
	if health.AppID != p.AppID || len(health.ServingDeploymentIDs) > 1 {
		return invalid("plan_stale", "app serving baseline changed since publication review")
	}
	if len(health.ServingDeploymentIDs) == 1 {
		serving = health.ServingDeploymentIDs[0]
	}
	if serving != p.Serving {
		return invalid("plan_stale", "serving release changed since publication review")
	}
	return nil
}

type publicationCLIReceipt struct {
	App              string                 `json:"app"`
	DeploymentID     string                 `json:"deployment_id"`
	Endpoint         string                 `json:"endpoint"`
	AuthMode         string                 `json:"auth_mode"`
	Promoted         bool                   `json:"promoted"`
	Verification     mcphosting.Report      `json:"verification"`
	SmokeTests       mcphosting.SmokeReport `json:"smoke_tests"`
	CatalogIntegrity struct {
		DeploymentID string `json:"deployment_id"`
		Pin          struct {
			SHA256 string `json:"sha256"`
		} `json:"pin"`
		Health struct {
			Healthy bool `json:"healthy"`
		} `json:"health"`
	} `json:"catalog_integrity"`
	ProtocolIntegrity struct {
		Qualification struct {
			Qualified bool `json:"qualified"`
		} `json:"qualification"`
	} `json:"protocol_integrity"`
}

func (m *management) runToolPublication(ctx context.Context, p *toolPublication, source string) {
	source = filepath.Join(source, "project")
	args := []string{"mcp", "deploy", "--path", source, "--name", p.App, "--fixtures", filepath.Join(source, filepath.FromSlash(p.Fixtures)),
		"--allow-tool-execution", "--timeout", "1200", "--idempotency-key", "mcp-tool-publish-" + p.ID, "--require-catalog-pin", "--require-reviewed-tools", "--expected-serving-deployment", p.Serving}
	if p.AppID != "" {
		args = append(args, "--expected-app-id", p.AppID)
	}
	if p.TokenEnv != "" {
		args = append(args, "--token-env", p.TokenEnv)
	}
	cli, err := m.opts.LocalCLI(ctx, args, nil)
	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	status := "outcome_unknown"
	var result *toolPublicationResult
	var raw publicationCLIReceipt
	appID := p.AppID
	if err == nil && json.Unmarshal(cli.Stdout, &raw) == nil && raw.App == p.App && validateSegment(raw.DeploymentID, "deployment_id") == nil {
		summary := mcphosting.SmokeReport{OK: raw.SmokeTests.OK}
		for _, check := range raw.SmokeTests.Checks {
			summary.Checks = append(summary.Checks, mcphosting.Check{Name: check.Name, Status: check.Status})
		}
		result = &toolPublicationResult{DeploymentID: raw.DeploymentID, Endpoint: raw.Endpoint, AuthMode: raw.AuthMode, Promoted: raw.Promoted,
			CatalogSHA256: raw.CatalogIntegrity.Pin.SHA256, ProtocolVerified: raw.Verification.OK && raw.ProtocolIntegrity.Qualification.Qualified, SmokeTests: summary}
		appID, err = m.confirmToolPublication(ctx, p, raw)
		if err == nil {
			status = "failed"
			if cli.ExitCode == 0 && raw.Promoted && raw.Verification.OK && publicationSmokePassed(p.CaseNames, raw.SmokeTests) && raw.CatalogIntegrity.Health.Healthy && raw.ProtocolIntegrity.Qualification.Qualified {
				status = "completed"
			} else if cli.ExitCode == 0 || raw.Promoted {
				err = invalid("invalid_response", "publication receipt lacks required verification evidence; inspect the exact deployment")
			}
		}
	} else if err == nil {
		err = invalid("publication_outcome_unknown", "publication returned no bound receipt; inspect native deployments before creating work")
	}
	if err != nil {
		status = "outcome_unknown"
		result = nil // Unbound CLI claims must not cross the account/app boundary.
	}
	if persistErr := m.persistToolPublication(ctx, p, status, raw); persistErr != nil {
		err = persistErr
		status, result = "outcome_unknown", nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if appID != "" {
		p.AppID = appID
	}
	p.Status, p.Result, p.ExpiresAt = status, result, time.Now().Add(24*time.Hour)
	if err != nil {
		problem := m.toolError(err).StructuredContent.(ToolError)
		problem.Outcome = "outcome_unknown"
		problem.Recovery = Recovery{Action: "inspect", Tool: "gregale_get_tool_deploy", Arguments: map[string]any{"plan_id": p.ID}}
		p.Problem = &problem
	}
}

func (m *management) confirmToolPublication(ctx context.Context, p *toolPublication, r publicationCLIReceipt) (string, error) {
	u, err := url.Parse(r.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", invalid("invalid_response", "publication endpoint must be a credential-free canonical HTTPS URL")
	}
	app, err := m.client.GetApp(ctx, p.App)
	if err != nil {
		return "", err
	}
	if app.ID == "" || app.Slug != p.App || p.AppID != "" && app.ID != p.AppID {
		return "", invalid("invalid_response", "publication app identity changed")
	}
	dep, err := m.client.GetDeployment(ctx, r.DeploymentID)
	if err != nil {
		return app.ID, err
	}
	if dep.ID != r.DeploymentID || dep.AppID != app.ID || normalizedScope(dep.Scope) != api.DefaultEnvScope || r.AuthMode != p.AccessMode {
		return app.ID, invalid("invalid_response", "publication receipt belongs to another app, scope or access mode")
	}
	if r.Promoted {
		if dep.Status != "live" || dep.TrafficPercent != 100 || r.CatalogIntegrity.DeploymentID != dep.ID || len(r.CatalogIntegrity.Pin.SHA256) != 64 || !slices.Contains(dep.OverrideEnvKeys, "MCP_CATALOG_EXPECTED_SHA256") {
			return app.ID, invalid("invalid_response", "published deployment lacks its verified catalog pin or serving state")
		}
		if _, err := hexDigest(r.CatalogIntegrity.Pin.SHA256); err != nil {
			return app.ID, err
		}
	}
	return app.ID, nil
}

func publicationSmokePassed(names []string, report mcphosting.SmokeReport) bool {
	if !report.OK || len(names) != len(report.Checks) {
		return false
	}
	for i, check := range report.Checks {
		if check.Name != names[i] || check.Status != "passed" {
			return false
		}
	}
	return true
}

func hexDigest(s string) ([]byte, error) {
	decoded, err := hex.DecodeString(s)
	if err != nil || len(decoded) != 32 {
		return nil, invalid("invalid_response", "catalog pin must be a SHA-256 hex digest")
	}
	return decoded, nil
}
