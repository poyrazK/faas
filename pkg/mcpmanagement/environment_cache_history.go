package mcpmanagement

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

type environmentCacheHistoryArgs struct {
	App            string `json:"app" jsonschema:"Gregale app slug"`
	Project        string `json:"project" jsonschema:"Project owning the named environment"`
	Environment    string `json:"environment" jsonschema:"Registered named environment; read consent required"`
	BeforeRevision int64  `json:"before_revision,omitempty" jsonschema:"Exclusive revision cursor; zero or omitted starts at newest"`
	Limit          int    `json:"limit,omitempty" jsonschema:"Page size from 1 to 50; defaults to 20"`
}
type environmentCacheRevisionArgs struct {
	App         string `json:"app" jsonschema:"Gregale app slug"`
	Project     string `json:"project" jsonschema:"Project owning the named environment"`
	Environment string `json:"environment" jsonschema:"Registered named environment; read consent required"`
	Revision    int64  `json:"revision" jsonschema:"Exact historical revision; zero selects initial live app inheritance"`
}
type environmentCacheRollbackArgs struct {
	App         string `json:"app" jsonschema:"Gregale app slug"`
	Project     string `json:"project" jsonschema:"Project owning the named environment"`
	Environment string `json:"environment" jsonschema:"Registered named environment"`
	Revision    int64  `json:"revision" jsonschema:"Retained historical revision to restore; zero selects live app inheritance"`
	Reason      string `json:"reason" jsonschema:"Reviewable rollback reason, at most 280 characters"`
}

func (m *management) listEnvironmentCacheHistory(ctx context.Context, args environmentCacheHistoryArgs) (any, error) {
	if args.Limit == 0 {
		args.Limit = api.EnvironmentCacheHistoryDefaultLimit
	}
	if args.BeforeRevision < 0 || args.BeforeRevision > api.RouteRequirementsMaxRevision || args.Limit < 1 || args.Limit > api.EnvironmentCacheHistoryMaxLimit || args.Environment == "" {
		return nil, invalid("invalid_argument", "select a named environment and bounded history page")
	}
	app, env, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, false)
	if err != nil {
		return nil, err
	}
	out, err := m.client.ListEnvironmentCachePolicyHistory(ctx, args.App, args.Environment, args.BeforeRevision, args.Limit)
	if err != nil {
		return nil, err
	}
	if out.AppID != app.ID || out.ProjectID != env.ProjectID || out.EnvironmentID != env.ID || out.Scope != args.Environment || out.CurrentRevision < 0 || out.CurrentRevision > api.RouteRequirementsMaxRevision || len(out.Entries) < 1 || len(out.Entries) > args.Limit {
		return nil, invalid("invalid_response", "cache history identity or page is invalid")
	}
	previous := api.RouteRequirementsMaxRevision + 1
	for _, p := range out.Entries {
		if validateEnvironmentCachePolicy(p, app.ID, args.Environment, env.ProjectID, env.ID) != nil || p.Revision > out.CurrentRevision || p.Revision >= previous || args.BeforeRevision > 0 && p.Revision >= args.BeforeRevision {
			return nil, invalid("invalid_response", "cache history entries are invalid")
		}
		previous = p.Revision
	}
	if next := out.NextBeforeRevision; next != nil && (*next != previous || *next < 1 || len(out.Entries) != args.Limit) {
		return nil, invalid("invalid_response", "cache history cursor is invalid")
	}
	fresh, freshEnv, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, false)
	if err != nil {
		return nil, err
	}
	if fresh.ID != app.ID || freshEnv.ID != env.ID || freshEnv.ProjectID != env.ProjectID {
		return nil, invalid("plan_stale", "environment identity changed during history read")
	}
	return out, nil
}
func (m *management) getEnvironmentCacheRevision(ctx context.Context, args environmentCacheRevisionArgs) (any, error) {
	if args.Environment == "" || args.Revision < 0 || args.Revision > api.RouteRequirementsMaxRevision {
		return nil, invalid("invalid_argument", "select a named environment and bounded historical revision")
	}
	app, env, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, false)
	if err != nil {
		return nil, err
	}
	out, err := m.client.GetEnvironmentCachePolicyRevision(ctx, args.App, args.Environment, args.Revision)
	if err != nil {
		return nil, err
	}
	if err = validateEnvironmentCachePolicy(out, app.ID, args.Environment, env.ProjectID, env.ID); err != nil {
		return nil, err
	}
	if out.Revision != args.Revision {
		return nil, invalid("invalid_response", "historical revision differs from requested revision")
	}
	fresh, freshEnv, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, false)
	if err != nil {
		return nil, err
	}
	if fresh.ID != app.ID || freshEnv.ID != env.ID || freshEnv.ProjectID != env.ProjectID {
		return nil, invalid("plan_stale", "environment identity changed during history read")
	}
	return out, nil
}
func (m *management) planEnvironmentCacheRollback(ctx context.Context, args environmentCacheRollbackArgs) (any, error) {
	if err := validateReason(args.App, args.Reason); err != nil {
		return nil, err
	}
	value, err := m.getEnvironmentCacheRevision(ctx, environmentCacheRevisionArgs{App: args.App, Project: args.Project, Environment: args.Environment, Revision: args.Revision})
	if err != nil {
		return nil, err
	}
	historical := value.(api.EnvironmentCachePolicy)
	p, err := m.newEnvironmentCacheChangePlan(ctx, environmentCachePlanArgs{App: args.App, Project: args.Project, Environment: args.Environment, Reason: args.Reason, Rules: historical.Rules}, historical.Inherited)
	if err != nil {
		return nil, err
	}
	p.Action = "environment_cache_rollback"
	p.EnvironmentCache.Restore = &historical
	p.EnvironmentCache.Proposed.RestoreRevision = &historical.Revision
	p.TrafficEffect = "Restores the reviewed historical cache mode and rules as a new monotonic revision in this named environment. Current revision and retained historical contents are checked atomically. Inherited mode follows live app rules, not historical app-rule bodies. Retry the same plan after a lost reply and watch the returned new revision for propagation."
	if err = validateEnvironmentCachePlan(p); err != nil {
		return nil, err
	}
	return m.savePlan(p)
}
func (m *management) applyEnvironmentCacheRollback(ctx context.Context, args applyArgs) (any, error) {
	return m.applyEnvironmentCacheChange(ctx, args, "environment_cache_rollback")
}
