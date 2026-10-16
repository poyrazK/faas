package mcpmanagement

import (
	"context"
	"reflect"

	"github.com/onebox-faas/faas/pkg/api"
)

type environmentCacheCopyArgs struct {
	App               string `json:"app" jsonschema:"Gregale app slug; source and destination belong to this same app"`
	Project           string `json:"project" jsonschema:"Project owning both named environments"`
	SourceEnvironment string `json:"source_environment" jsonschema:"Named environment with an explicit cache override to copy; read consent required"`
	Environment       string `json:"environment" jsonschema:"Destination named environment; apply requires write consent"`
	Reason            string `json:"reason" jsonschema:"Reviewable reason, at most 280 characters"`
}

func (m *management) planEnvironmentCacheCopy(ctx context.Context, args environmentCacheCopyArgs) (any, error) {
	if err := validateReason(args.App, args.Reason); err != nil {
		return nil, err
	}
	if args.SourceEnvironment == "" || args.Environment == "" || args.SourceEnvironment == args.Environment {
		return nil, invalid("invalid_argument", "select two different named environments of the same app")
	}
	value, err := m.getEnvironmentCachePolicy(ctx, appArgs{App: args.App, Project: args.Project, Environment: args.SourceEnvironment})
	if err != nil {
		return nil, err
	}
	source := value.(api.EnvironmentCachePolicy)
	if source.Inherited {
		return nil, invalid("unsupported_scope", "copy requires an explicit source override; inherited app rules are not copied")
	}
	p, err := m.newEnvironmentCacheChangePlan(ctx, environmentCachePlanArgs{App: args.App, Project: args.Project, Environment: args.Environment, Reason: args.Reason, Rules: source.Rules}, false)
	if err != nil {
		return nil, err
	}
	p.Action = "environment_cache_copy"
	p.EnvironmentCache.Source = &source
	p.EnvironmentCache.Proposed.CopyFrom = &api.EnvironmentCachePolicySource{Scope: source.Scope, EnvironmentID: source.EnvironmentID, Revision: source.Revision}
	p.TrafficEffect = "Copies the reviewed explicit source cache rules to the destination immutable environment host. Both identities and revisions are guarded atomically; source policy and other environments stay unchanged. An empty explicit source disables destination caching. This creates an independent override, not ongoing synchronization. Watch the returned destination revision for propagation."
	if err = validateEnvironmentCachePlan(p); err != nil {
		return nil, err
	}
	return m.savePlan(p)
}
func (m *management) applyEnvironmentCacheCopy(ctx context.Context, args applyArgs) (any, error) {
	return m.applyEnvironmentCacheChange(ctx, args, "environment_cache_copy")
}

func validateEnvironmentCachePlan(p *releasePlan) error {
	c := p.EnvironmentCache
	if c == nil || api.ValidateEnvironmentCachePolicyRequest(c.Proposed) != nil || validateEnvironmentCachePolicy(c.Before, p.AppID, p.Scope, p.ProjectID, p.EnvironmentID) != nil || c.Proposed.AppID != p.AppID || c.Proposed.ProjectID != p.ProjectID || c.Proposed.EnvironmentID != p.EnvironmentID || *c.Proposed.ExpectedRevision != c.Before.Revision {
		return invalid("invalid_plan", "environment cache review binding is invalid")
	}
	expectedInherited := p.Action == "environment_cache_inheritance"
	rollback := p.Action == "environment_cache_rollback"
	if (c.Restore != nil) != rollback || (c.Proposed.RestoreRevision != nil) != rollback {
		return invalid("invalid_plan", "cache rollback binding is invalid")
	}
	if rollback {
		h := c.Restore
		if validateEnvironmentCachePolicy(*h, p.AppID, p.Scope, p.ProjectID, p.EnvironmentID) != nil || h.Revision != *c.Proposed.RestoreRevision || h.Revision > c.Before.Revision || !reflect.DeepEqual(h.Rules, c.Proposed.Rules) {
			return invalid("invalid_plan", "rollback differs from reviewed history")
		}
		expectedInherited = h.Inherited
	}
	if c.Proposed.Inherited != expectedInherited {
		return invalid("invalid_plan", "cache mode differs from reviewed action")
	}
	copyAction := p.Action == "environment_cache_copy"
	if (c.Source != nil) != copyAction || (c.Proposed.CopyFrom != nil) != copyAction {
		return invalid("invalid_plan", "cache copy source binding is invalid")
	}
	if copyAction {
		s, pin := c.Source, c.Proposed.CopyFrom
		if validateEnvironmentCachePolicy(*s, p.AppID, pin.Scope, p.ProjectID, pin.EnvironmentID) != nil || s.Inherited || s.Revision != pin.Revision || !reflect.DeepEqual(s.Rules, c.Proposed.Rules) {
			return invalid("invalid_plan", "cache copy source differs from reviewed replacement")
		}
	}
	return nil
}

// Check source read consent even when recovering a previously completed receipt.
// The native transaction owns revision checks and original receipt recovery.
func (m *management) environmentCacheCopyConsent(ctx context.Context, p *releasePlan) error {
	if p.Action != "environment_cache_copy" {
		return nil
	}
	source := p.EnvironmentCache.Source
	app, env, err := m.appEnvironment(ctx, p.App, p.Project, source.Scope, false)
	if err != nil {
		return err
	}
	if app.ID != p.AppID || env.ProjectID != p.ProjectID || env.ID != source.EnvironmentID {
		return invalid("plan_stale", "cache copy source identity changed; create a new plan")
	}
	return nil
}
