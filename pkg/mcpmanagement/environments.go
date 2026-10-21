package mcpmanagement

import (
	"context"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
	"sort"
)

type environmentArgs struct {
	Project     string `json:"project" jsonschema:"Gregale project slug"`
	Environment string `json:"environment" jsonschema:"Registered named environment, such as staging or production"`
}

type environmentPromotionArgs struct {
	Project     string `json:"project" jsonschema:"Gregale project slug"`
	Environment string `json:"environment" jsonschema:"Target named environment"`
	PromotionID string `json:"promotion_id" jsonschema:"Exact durable environment promotion ID"`
}

func registerEnvironments(s *mcp.Server, m *management) {
	register(s, m, "gregale_list_environments", "List consented project environments and whether this connection can write each. Named access pins existing project, environment and workload identities.", m.listEnvironments)
	register(s, m, "gregale_get_environment", "Inspect non-secret release inventory in one named project environment. Only consented workloads are returned; this is release state, not health evidence.", m.getEnvironment)
	register(s, m, "gregale_get_environment_promotion", "Inspect a durable environment promotion and rollback. Requires consent for its source, target, and every project workload.", m.getEnvironmentPromotion)
	register(s, m, "gregale_plan_environment_promotion", "Review an exact source-to-target environment release promotion. Reuses immutable artifacts and target configuration; qualifications and protected-environment approval remain required by Gregale.", m.planEnvironmentPromotion)
	register(s, m, "gregale_plan_environment_rollback", "Review rollback of one exact environment promotion to its captured predecessor. Gregale checks that target releases have not changed.", m.planEnvironmentRollback)
	if m.opts.AllowWrites {
		registerTool(s, m, "gregale_apply_environment_promotion", "Apply an unexpired environment promotion plan with a stable retry key. Changes target traffic and can incur costs. Protected targets return approval_pending with a human review_url. Ask the human to review in Gregale, then retry this same plan_id at the returned interval; approval credentials stay private.", false, m.applyEnvironmentPromotion)
		registerTool(s, m, "gregale_apply_environment_rollback", "Apply a reviewed environment rollback using a stable retry key and Gregale's captured-release guards. Changes target traffic.", false, m.applyEnvironmentRollback)
	}
}

func (m *management) listEnvironments(ctx context.Context, _ noArgs) (any, error) {
	grants := []EnvironmentGrant{}
	if m.opts.Environments != nil {
		for _, g := range m.opts.Environments.Named {
			if _, _, err := m.environmentAccess(ctx, g.Project, g.Environment, false, false); err != nil {
				return nil, err
			}
			g.Write = g.Write && m.opts.AllowWrites
			grants = append(grants, g)
		}
	} else {
		var err error
		grants, err = discoverEnvironments(ctx, m.client)
		if err != nil {
			return nil, err
		}
		for i := range grants {
			grants[i].Write = m.opts.AllowWrites
		}
	}
	sort.Slice(grants, func(i, j int) bool {
		return grants[i].Project+"/"+grants[i].Environment < grants[j].Project+"/"+grants[j].Environment
	})
	return map[string]any{"items": grants, "default_write": m.opts.AllowWrites && (m.opts.Environments == nil || m.opts.Environments.DefaultWrite)}, nil
}

func (m *management) getEnvironment(ctx context.Context, args environmentArgs) (any, error) {
	p, e, err := m.environmentAccess(ctx, args.Project, args.Environment, false, false)
	if err != nil {
		return nil, err
	}
	releases, err := m.client.GetProjectEnvironmentReleases(ctx, args.Project, args.Environment)
	if err != nil {
		return nil, err
	}
	if releases.ProjectSlug != args.Project || releases.Environment != args.Environment {
		return nil, invalid("invalid_response", "release inventory environment mismatch")
	}
	items := []api.ProjectEnvironmentReleaseWorkloadResponse{}
	for _, w := range releases.Workloads {
		member := false
		for _, a := range p.Workloads {
			member = member || a.Slug == w.WorkloadSlug
		}
		if !member {
			return nil, invalid("invalid_response", "release inventory workload mismatch")
		}
		if m.opts.AllowedApps != nil && m.opts.AllowedApps[w.WorkloadSlug] == "" {
			continue
		}
		if m.opts.Environments != nil && m.opts.Environments.Named[args.Project+"/"+args.Environment].Apps[w.WorkloadSlug] == "" {
			continue
		}
		a, err := m.client.GetApp(ctx, w.WorkloadSlug)
		if err != nil {
			return nil, err
		}
		if !m.appIDAllowed(a.ID) || (m.opts.Environments != nil && m.opts.Environments.Named[args.Project+"/"+args.Environment].Apps[w.WorkloadSlug] != a.ID) {
			return nil, invalid("app_access_denied", "workload identity changed; reconnect")
		}
		items = append(items, w)
	}
	return map[string]any{"project": p.Slug, "project_id": p.ID, "environment": e.Slug, "environment_id": e.ID, "protected": e.Protected, "workloads": items}, nil
}

func (m *management) getEnvironmentPromotion(ctx context.Context, args environmentPromotionArgs) (any, error) {
	if validateSegment(args.PromotionID, "promotion_id") != nil {
		return nil, invalid("invalid_argument", "invalid promotion ID")
	}
	if _, _, err := m.environmentAccess(ctx, args.Project, args.Environment, false, true); err != nil {
		return nil, err
	}
	status, err := m.client.GetProjectEnvironmentPromotionStatus(ctx, args.Project, args.Environment, args.PromotionID)
	if err != nil {
		return nil, err
	}
	if status.PromotionID != args.PromotionID || status.ProjectSlug != args.Project || status.ToEnvironment != args.Environment {
		return nil, invalid("invalid_response", "promotion identity mismatch")
	}
	if _, _, err := m.environmentAccess(ctx, args.Project, status.FromEnvironment, false, true); err != nil {
		return nil, err
	}
	// Recheck both environments after the response, before exposing history.
	if _, _, err := m.environmentAccess(ctx, args.Project, args.Environment, false, true); err != nil {
		return nil, err
	}
	return status, nil
}
