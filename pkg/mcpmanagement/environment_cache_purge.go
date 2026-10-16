package mcpmanagement

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type environmentPurgePlanArgs struct {
	App         string `json:"app" jsonschema:"Gregale app slug"`
	Project     string `json:"project" jsonschema:"Project owning the named environment"`
	Environment string `json:"environment" jsonschema:"Registered named environment"`
	Path        string `json:"path" jsonschema:"Exact decoded URL path without query, fragment or wildcards; empty string purges the entire immutable environment host"`
	Reason      string `json:"reason" jsonschema:"Reviewable purge reason, at most 280 characters"`
}
type environmentPurgeStatusArgs struct {
	App         string `json:"app" jsonschema:"Gregale app slug"`
	Project     string `json:"project" jsonschema:"Project owning the named environment"`
	Environment string `json:"environment" jsonschema:"Registered named environment; read consent required"`
	RequestID   string `json:"request_id" jsonschema:"Exact purge request UUID returned by apply"`
}
type environmentPurgePlan struct {
	Policy   api.EnvironmentCachePolicy             `json:"current_policy"`
	Proposed api.CreateEnvironmentCachePurgeRequest `json:"proposed"`
}

func validatePurgeReceipt(out api.EnvironmentCachePurgeReceipt, app, scope, project, environment, request string) error {
	if out.AppID != app || out.Scope != scope || out.ProjectID != project || out.EnvironmentID != environment || out.RequestID != request || out.Generation < 1 || out.Generation > api.RouteRequirementsMaxRevision || out.RequestedAt.IsZero() || api.ValidateEnvironmentCachePurgePath(out.Path) != nil {
		return invalid("invalid_response", "purge receipt lacks exact identity or generation")
	}
	return nil
}
func validateEnvironmentPurgePlan(p *releasePlan) error {
	c := p.EnvironmentPurge
	if c == nil || api.ValidateEnvironmentCachePurgeRequest(c.Proposed) != nil || c.Proposed.AppID != p.AppID || c.Proposed.ProjectID != p.ProjectID || c.Proposed.EnvironmentID != p.EnvironmentID || validateEnvironmentCachePolicy(c.Policy, p.AppID, p.Scope, p.ProjectID, p.EnvironmentID) != nil {
		return invalid("invalid_plan", "purge review binding is invalid")
	}
	return nil
}
func (m *management) planEnvironmentCachePurge(ctx context.Context, args environmentPurgePlanArgs) (any, error) {
	if err := validateReason(args.App, args.Reason); err != nil {
		return nil, err
	}
	if err := api.ValidateEnvironmentCachePurgePath(args.Path); err != nil {
		return nil, invalid("invalid_argument", err.Error())
	}
	value, err := m.getEnvironmentCachePolicy(ctx, appArgs{App: args.App, Project: args.Project, Environment: args.Environment})
	if err != nil {
		return nil, err
	}
	policy := value.(api.EnvironmentCachePolicy)
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return nil, err
	}
	if account.ID == "" {
		return nil, invalid("invalid_response", "account identity is missing")
	}
	req := api.CreateEnvironmentCachePurgeRequest{AppID: policy.AppID, ProjectID: policy.ProjectID, EnvironmentID: policy.EnvironmentID, RequestID: uuid.NewString(), Path: args.Path}
	p := &releasePlan{Action: "environment_cache_purge", AccountID: account.ID, APIBase: m.opts.APIBase, Profile: m.opts.Profile, AppID: policy.AppID, App: args.App, Project: args.Project, ProjectID: policy.ProjectID, EnvironmentID: policy.EnvironmentID, Scope: policy.Scope, Reason: args.Reason, ExpiresAt: time.Now().Add(releasePlanTTL), TrafficEffect: "Logically invalidates cached responses for this immutable environment host and exact path (all queries, methods, variants and cache rules), or the entire environment when path is empty. Other environments and policy stay unchanged. Prior-generation responses become unreachable after gateway replay, including late in-flight cache writes. Redis objects age out by TTL; receipt confirms intent, not completion. Use purge status to verify registered gateways.", EnvironmentPurge: &environmentPurgePlan{Policy: policy, Proposed: req}}
	if err = validateEnvironmentPurgePlan(p); err != nil {
		return nil, err
	}
	return m.savePlan(p)
}
func (m *management) applyEnvironmentCachePurge(ctx context.Context, args applyArgs) (any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.checkedPlan(ctx, args.PlanID, "environment_cache_purge")
	if err != nil {
		return nil, err
	}
	if err = validateEnvironmentPurgePlan(p); err != nil {
		return nil, err
	}
	if p.environmentPurgeResult != nil {
		return *p.environmentPurgeResult, nil
	}
	p.attempted = true
	if err = m.persistPlans(); err != nil {
		return nil, err
	}
	out, err := m.client.CreateEnvironmentCachePurge(ctx, p.App, p.Scope, p.EnvironmentPurge.Proposed)
	if err != nil {
		return nil, err
	}
	if err = validatePurgeReceipt(out, p.AppID, p.Scope, p.ProjectID, p.EnvironmentID, p.EnvironmentPurge.Proposed.RequestID); err != nil {
		return nil, err
	}
	if out.Path != p.EnvironmentPurge.Proposed.Path {
		return nil, invalid("invalid_response", "purge target differs from review")
	}
	p.environmentPurgeResult = &out
	if err = m.persistPlans(); err != nil {
		return nil, err
	}
	return out, nil
}
func (m *management) getEnvironmentCachePurge(ctx context.Context, args environmentPurgeStatusArgs) (any, error) {
	if _, err := uuid.Parse(args.RequestID); err != nil || args.Environment == "" {
		return nil, invalid("invalid_argument", "select a named environment and purge UUID")
	}
	app, env, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, false)
	if err != nil {
		return nil, err
	}
	out, err := m.client.GetEnvironmentCachePurge(ctx, args.App, args.Environment, args.RequestID)
	if err != nil {
		return nil, err
	}
	if err = validatePurgeReceipt(out.EnvironmentCachePurgeReceipt, app.ID, args.Environment, env.ProjectID, env.ID, args.RequestID); err != nil {
		return nil, err
	}
	evidence := api.EnvironmentCacheConvergence{AppID: out.AppID, ProjectID: out.ProjectID, EnvironmentID: out.EnvironmentID, Scope: out.Scope, Revision: out.Generation, Status: out.Status, CheckedAt: out.CheckedAt, ServingGateways: out.ServingGateways, AcknowledgedGateways: out.AcknowledgedGateways, PendingGateways: out.PendingGateways, StaleGateways: out.StaleGateways}
	if evidence.Status == "completed" {
		evidence.Status = "converged"
	}
	if err = validateCacheConvergence(evidence, app.ID, args.Environment, env.ProjectID, env.ID); err != nil {
		return nil, err
	}
	if out.Status != "completed" && out.Status != "pending" && out.Status != "degraded" {
		return nil, invalid("invalid_response", "invalid purge status")
	}
	fresh, freshEnv, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, false)
	if err != nil {
		return nil, err
	}
	if fresh.ID != app.ID || freshEnv.ID != env.ID || freshEnv.ProjectID != env.ProjectID {
		return nil, invalid("plan_stale", "environment identity changed during purge inspection")
	}
	return out, nil
}
