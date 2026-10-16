package mcpmanagement

import (
	"context"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

type environmentCachePlanArgs struct {
	App         string                     `json:"app" jsonschema:"Gregale app slug"`
	Project     string                     `json:"project" jsonschema:"Project owning the selected environment"`
	Environment string                     `json:"environment" jsonschema:"Registered named environment; required"`
	Reason      string                     `json:"reason" jsonschema:"Reviewable reason, at most 280 characters"`
	Rules       []api.EnvironmentCacheRule `json:"rules" jsonschema:"Complete replacement cache rules, up to 20 with distinct priorities; [] disables caching in this environment"`
}

type environmentCachePlan struct {
	Restore  *api.EnvironmentCachePolicy          `json:"restore_from,omitempty"`
	Source   *api.EnvironmentCachePolicy          `json:"copy_source,omitempty"`
	Before   api.EnvironmentCachePolicy           `json:"before"`
	Proposed api.SetEnvironmentCachePolicyRequest `json:"proposed"`
}

type environmentCacheInheritanceArgs struct {
	App         string `json:"app" jsonschema:"Gregale app slug"`
	Project     string `json:"project" jsonschema:"Project owning the selected environment"`
	Environment string `json:"environment" jsonschema:"Registered named environment; required"`
	Reason      string `json:"reason" jsonschema:"Reviewable reason, at most 280 characters"`
}

func registerEnvironmentCachePolicy(s *mcp.Server, m *management) {
	register(s, m, "gregale_recover_environment_cache_schedule_activity", "Rebuild a current schedule baseline using bounded pages and a resumable recovery_token. Captures an activity checkpoint before discovery and preserves it across pages. Current named-environment read consent required. Complete pages return activity_cursor for catch-up/watch. recovery_restart_required means discard the partial baseline and restart without a token. Live baseline, not a frozen snapshot or restored history; refresh exact status for overlapping events. No writes.", m.recoverEnvironmentCacheScheduleActivity)
	register(s, m, "gregale_get_environment_cache_schedule_activity", "Catch up on durable cache schedule acceptance, replacement, cancellation and execution outcomes using a saved scoped cursor. Current named-environment read consent required. Returns compact immutable events, failure codes and receipt revisions/generations; execution is separate from gateway convergence. Thirty-day retention; cursor_expired includes scoped recovery metadata. Save restart_cursor before rebuilding the current schedule baseline. No writes or push notifications.", m.getEnvironmentCacheScheduleActivity)
	register(s, m, "gregale_watch_environment_cache_schedule_activity", "Wait for one bounded page of durable schedule activity after a scoped cursor. Rechecks current named-environment read consent on every observation; timeout preserves the checkpoint. Cancellation can be retried with the same cursor. Execution outcomes are separate from gateway convergence. Thirty-day retention; cursor_expired requires saving restart_cursor before rebuilding the current schedule baseline.", m.watchEnvironmentCacheScheduleActivity)
	register(s, m, "gregale_list_environment_cache_schedules", "Discover one named environment's schedules in execution-time order with status/time filters and bounded cursor pages. Read consent required. Live pages are not a frozen snapshot; use exact status for execution/convergence evidence.", m.listEnvironmentCacheSchedules)
	register(s, m, "gregale_plan_environment_cache_reschedule", "Review changing only the time of a pending schedule. Pins original UUID, immutable target, action, original policy guard and new time; no writes. Executing, terminal or stale policy actions cannot be rescheduled.", m.planEnvironmentCacheReschedule)
	register(s, m, "gregale_plan_environment_cache_schedule", "Review one cache-policy replacement, inheritance restore or exact-path/whole-environment purge at a future timestamp within 30 days. Pins exact identities and policy revision. No writes; review expires after 15 minutes.", m.planEnvironmentCacheSchedule)
	register(s, m, "gregale_get_environment_cache_schedule", "Read a consented named environment schedule and its execution status. Executed schedules include separate current gateway convergence evidence; acceptance and execution do not establish convergence.", m.getEnvironmentCacheSchedule)
	register(s, m, "gregale_plan_environment_cache_purge", "Review an exact-path or whole immutable environment-host cache purge. Requires named environment read consent; does not mutate policy or purge data. Empty path selects the whole environment. Pins identities and request UUID for 15 minutes.", m.planEnvironmentCachePurge)
	register(s, m, "gregale_get_environment_cache_purge", "Read an exact purge request and current-session gateway acknowledgements. Reports completed, pending or degraded; stale/zero gateway fleets never count as completed. Read-only and scoped to the consented named environment.", m.getEnvironmentCachePurge)
	register(s, m, "gregale_list_environment_cache_history", "Read a bounded newest-first page of cache policy revisions for one consented named environment. Includes a synthetic initial inherited revision zero. Historical rule bodies require exact environment read consent; no writes.", m.listEnvironmentCacheHistory)
	register(s, m, "gregale_get_environment_cache_revision", "Read an exact retained cache policy revision for a consented named environment. Revision zero represents initial live inheritance, not a frozen app-rule snapshot. No writes.", m.getEnvironmentCacheRevision)
	register(s, m, "gregale_plan_environment_cache_rollback", "Review restoring a retained cache policy as a new revision, pinning immutable environment identity and current revision for 15 minutes. Supports overrides, cache-disabled and inherited modes. Read-only; inherited rollback follows live app rules.", m.planEnvironmentCacheRollback)
	register(s, m, "gregale_plan_environment_cache_copy", "Review copying an explicit cache override, including cache-disabled [], between two named environments of one app. Requires source and destination read consent. Pins both identities and revisions for 15 minutes; inherited sources are rejected. No writes.", m.planEnvironmentCacheCopy)
	register(s, m, "gregale_plan_environment_cache_inheritance", "Review removal of a named environment cache override, restoring live app cache inheritance at a new revision. Pins immutable identity and revision for 15 minutes. Read-only; inherited app rule bodies are not returned or frozen by this review.", m.planEnvironmentCacheInheritance)
	register(s, m, "gregale_watch_environment_cache_policy", "Verify an exact applied cache revision using fresh current-session acknowledgements from registered serving gateways. Reports pending, converged, degraded or timed_out. Zero or stale gateways never count as success. Read-only; rechecks exact environment consent and rejects superseded revisions.", m.watchEnvironmentCachePolicy)
	register(s, m, "gregale_get_environment_cache_policy", "Read the independent cache policy for one consented immutable named environment. inherited=true means app cache rules still apply; inherited rule bodies are not returned.", m.getEnvironmentCachePolicy)
	register(s, m, "gregale_plan_environment_cache_policy", "Review complete replacement cache rules for one named environment, pinning its identity and policy revision for 15 minutes. [] disables caching there. No writes or changes to other environments; app-owned cache settings are shadowed after gateway reload.", m.planEnvironmentCachePolicy)
	if m.opts.AllowWrites {
		registerTool(s, m, "gregale_apply_environment_cache_reschedule", "Atomically cancel a reviewed pending schedule and create its linked replacement under current named environment write consent. Reject worker/cancellation/replacement races. Retry the same plan after reply loss; no action is executed by rescheduling.", false, m.applyEnvironmentCacheReschedule)
		registerTool(s, m, "gregale_apply_environment_cache_schedule", "Accept a reviewed one-time cache schedule with current named environment write consent. Durable apid delegation survives MCP disconnect/token revocation; cancel explicitly before worker claim. Retry the same plan after a lost reply.", false, m.applyEnvironmentCacheSchedule)
		registerTool(s, m, "gregale_cancel_environment_cache_schedule", "Cancel the exact schedule UUID in a write-consented named environment before worker claim. Idempotent after cancellation; executing or terminal actions reject cancellation. Does not reverse an executed action.", false, m.cancelEnvironmentCacheSchedule)
		registerTool(s, m, "gregale_apply_environment_cache_purge", "Commit a reviewed cache purge with current named environment write consent and an atomic retry-safe request UUID. Retry the same plan after an ambiguous reply; use purge status to verify gateway replay. No policy changes.", false, m.applyEnvironmentCachePurge)
		registerTool(s, m, "gregale_apply_environment_cache_rollback", "Apply a reviewed cache rollback with exact environment write consent and atomic historical-content and current-revision guards. Native intent, history and retry receipt commit together. Watch the returned new revision for propagation.", false, m.applyEnvironmentCacheRollback)
		registerTool(s, m, "gregale_apply_environment_cache_copy", "Apply a reviewed cache copy with source read consent and destination write consent. Native source and destination revision guards and retry receipt commit atomically. Retry the same plan after a lost reply; use the returned destination revision with the cache-policy watch.", false, m.applyEnvironmentCacheCopy)
		registerTool(s, m, "gregale_apply_environment_cache_inheritance", "Apply a reviewed restoration of app cache inheritance with exact environment write consent, revision guard and atomic retry receipt. Use the returned revision with the cache-policy watch to verify gateway propagation.", false, m.applyEnvironmentCacheInheritance)
		registerTool(s, m, "gregale_apply_environment_cache_policy", "Apply an environment cache-policy review with exact environment write consent and native revision guard. Retry the same plan after a lost reply: native policy and durable receipt commit atomically. Desired settings propagate on gateway policy reload; receipt does not prove convergence.", false, m.applyEnvironmentCachePolicy)
	}
}

func validateEnvironmentCachePolicy(out api.EnvironmentCachePolicy, app, scope, project, environment string) error {
	if out.AppID != app || out.Scope != scope || out.ProjectID != project || out.EnvironmentID != environment || environment == "" || !api.ValidProjectEnvironmentSlug(scope) || out.Revision < 0 || out.Revision > api.RouteRequirementsMaxRevision || !out.Inherited && out.Revision == 0 || out.Inherited && len(out.Rules) != 0 || out.Revision > 0 && (out.UpdatedAt == nil || out.UpdatedAt.IsZero()) {
		return invalid("invalid_response", "cache policy lacks exact environment identity or revision")
	}
	normalized, err := api.NormalizeEnvironmentCacheRules(out.Rules)
	if err != nil || !reflect.DeepEqual(normalized, out.Rules) {
		return invalid("invalid_response", "cache policy settings are invalid or not normalized")
	}
	return nil
}
func (m *management) getEnvironmentCachePolicy(ctx context.Context, args appArgs) (any, error) {
	if args.Environment == "" {
		return nil, invalid("unsupported_scope", "select a registered named environment for independent cache policy")
	}
	app, env, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, false)
	if err != nil {
		return nil, err
	}
	out, err := m.client.GetEnvironmentCachePolicy(ctx, args.App, args.Environment)
	if err != nil {
		return nil, err
	}
	if err = validateEnvironmentCachePolicy(out, app.ID, args.Environment, env.ProjectID, env.ID); err != nil {
		return nil, err
	}
	fresh, freshEnv, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, false)
	if err != nil {
		return nil, err
	}
	if fresh.ID != app.ID || freshEnv.ID != env.ID || freshEnv.ProjectID != env.ProjectID {
		return nil, invalid("plan_stale", "environment identity changed during inspection")
	}
	return out, nil
}
func (m *management) planEnvironmentCachePolicy(ctx context.Context, args environmentCachePlanArgs) (any, error) {
	return m.planEnvironmentCacheChange(ctx, args, false)
}
func (m *management) planEnvironmentCacheInheritance(ctx context.Context, args environmentCacheInheritanceArgs) (any, error) {
	return m.planEnvironmentCacheChange(ctx, environmentCachePlanArgs{App: args.App, Project: args.Project, Environment: args.Environment, Reason: args.Reason, Rules: []api.EnvironmentCacheRule{}}, true)
}
func (m *management) planEnvironmentCacheChange(ctx context.Context, args environmentCachePlanArgs, inherited bool) (any, error) {
	p, err := m.newEnvironmentCacheChangePlan(ctx, args, inherited)
	if err != nil {
		return nil, err
	}
	return m.savePlan(p)
}
func (m *management) newEnvironmentCacheChangePlan(ctx context.Context, args environmentCachePlanArgs, inherited bool) (*releasePlan, error) {
	if err := validateReason(args.App, args.Reason); err != nil {
		return nil, err
	}
	rules, err := api.NormalizeEnvironmentCacheRules(args.Rules)
	if err != nil {
		return nil, invalid("invalid_argument", err.Error())
	}
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return nil, err
	}
	if account.ID == "" {
		return nil, invalid("invalid_response", "account identity is missing")
	}
	value, err := m.getEnvironmentCachePolicy(ctx, appArgs{App: args.App, Project: args.Project, Environment: args.Environment})
	if err != nil {
		return nil, err
	}
	before := value.(api.EnvironmentCachePolicy)
	req := api.SetEnvironmentCachePolicyRequest{AppID: before.AppID, ProjectID: before.ProjectID, EnvironmentID: before.EnvironmentID, RequestID: uuid.NewString(), ExpectedRevision: &before.Revision, Rules: rules, Inherited: inherited}
	if err = api.ValidateEnvironmentCachePolicyRequest(req); err != nil {
		return nil, invalid("invalid_response", "native environment identities cannot authorize a cache policy write")
	}
	p := &releasePlan{Action: "environment_cache_policy", AccountID: account.ID, APIBase: m.opts.APIBase, Profile: m.opts.Profile, AppID: before.AppID, App: args.App, Project: args.Project, ProjectID: before.ProjectID, EnvironmentID: before.EnvironmentID, Scope: before.Scope, Reason: args.Reason, ExpiresAt: time.Now().Add(releasePlanTTL), TrafficEffect: "Replaces cache rules only for the selected immutable environment host, shadowing inherited cache rules. Other environments and rule kinds stay unchanged. Gateways reload desired policy on their normal cache TTL (30 seconds); receipt does not prove convergence. [] disables caching there.", EnvironmentCache: &environmentCachePlan{Before: before, Proposed: req}}
	if inherited {
		p.Action = "environment_cache_inheritance"
		p.TrafficEffect = "Restores live app cache inheritance only for the selected immutable environment host at a new revision. App rules are evaluated on each gateway reload, not frozen by this review. Other environments and rule kinds stay unchanged. The receipt records desired policy; watch the returned revision for gateway convergence."
	}
	return p, nil
}
func (m *management) applyEnvironmentCachePolicy(ctx context.Context, args applyArgs) (any, error) {
	return m.applyEnvironmentCacheChange(ctx, args, "environment_cache_policy")
}
func (m *management) applyEnvironmentCacheInheritance(ctx context.Context, args applyArgs) (any, error) {
	return m.applyEnvironmentCacheChange(ctx, args, "environment_cache_inheritance")
}
func (m *management) applyEnvironmentCacheChange(ctx context.Context, args applyArgs, action string) (any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.checkedPlan(ctx, args.PlanID, action)
	if err != nil {
		return nil, err
	}
	if err = validateEnvironmentCachePlan(p); err != nil {
		return nil, err
	}
	if err = m.environmentCacheCopyConsent(ctx, p); err != nil {
		return nil, err
	}
	if p.environmentCacheResult != nil {
		return *p.environmentCacheResult, nil
	}
	p.attempted = true
	if err = m.persistPlans(); err != nil {
		return nil, err
	}
	out, err := m.client.SetEnvironmentCachePolicy(ctx, p.App, p.Scope, p.EnvironmentCache.Proposed)
	if err != nil {
		return nil, err
	}
	if err = validateEnvironmentCachePolicy(out, p.AppID, p.Scope, p.ProjectID, p.EnvironmentID); err != nil {
		return nil, err
	}
	if out.Inherited != p.EnvironmentCache.Proposed.Inherited || out.Revision != *p.EnvironmentCache.Proposed.ExpectedRevision+1 || !reflect.DeepEqual(out.Rules, p.EnvironmentCache.Proposed.Rules) {
		return nil, invalid("invalid_response", "saved cache policy differs from the reviewed replacement")
	}
	p.environmentCacheResult = &out
	if err = m.persistPlans(); err != nil {
		return nil, err
	}
	return out, nil
}
