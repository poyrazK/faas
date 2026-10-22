package mcpmanagement

import (
	"context"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

type noArgs struct{}

type appArgs struct {
	App         string `json:"app" jsonschema:"Gregale app slug"`
	Project     string `json:"project,omitempty" jsonschema:"Project slug required with a named environment"`
	Environment string `json:"environment,omitempty" jsonschema:"Registered named environment; omit for default"`
}

type appsArgs struct {
	Limit  int `json:"limit,omitempty" jsonschema:"Page size from 1 to 100; default 20"`
	Offset int `json:"offset,omitempty" jsonschema:"Non-negative offset from a previous next_offset; default 0"`
}

type deploymentsArgs struct {
	Limit  int    `json:"limit,omitempty" jsonschema:"Page size from 1 to 100; default 20"`
	Before string `json:"before,omitempty" jsonschema:"Opaque next_before cursor from the previous page"`
}

type deploymentArgs struct {
	DeploymentID string `json:"deployment_id" jsonschema:"Deployment ID from gregale_list_deployments"`
}

type appView struct {
	ID                     string                        `json:"id"`
	Slug                   string                        `json:"slug"`
	Type                   string                        `json:"type"`
	Runtime                string                        `json:"runtime,omitempty"`
	Status                 string                        `json:"status"`
	URL                    string                        `json:"url"`
	CanonicalURL           string                        `json:"canonical_url"`
	RAMMB                  int                           `json:"ram_mb"`
	CPUMillicores          int                           `json:"cpu_millicores"`
	MaxConcurrency         int                           `json:"max_concurrency"`
	MinInstances           int                           `json:"min_instances"`
	DeploymentAvailability api.AppDeploymentAvailability `json:"deployment_availability"`
}

func projectApp(a api.AppResponse) appView {
	return appView{
		ID: a.ID, Slug: a.Slug, Type: a.Type, Runtime: a.Runtime, Status: a.Status,
		URL: a.URL, CanonicalURL: a.CanonicalURL, RAMMB: a.RAMMB, CPUMillicores: a.CPUMillicores,
		MaxConcurrency: a.MaxConcurrency, MinInstances: a.MinInstances, DeploymentAvailability: a.DeploymentAvailability,
	}
}

func (m *management) connection(ctx context.Context, _ noArgs) (any, error) {
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return nil, err
	}
	capabilities, err := m.client.GetCapabilities(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"profile": m.opts.Profile, "api_base": m.opts.APIBase, "read_only": !m.opts.AllowWrites,
		"account":            map[string]string{"id": account.ID, "plan": account.Plan, "status": account.Status},
		"capabilities":       capabilities,
		"allowed_apps":       m.opts.AllowedApps,
		"environment_access": m.opts.Environments,
		"request_access":     m.requestAccessMetadata(),
	}, nil
}

func (m *management) listApps(ctx context.Context, args appsArgs) (any, error) {
	limit, err := pageLimit(args.Limit)
	if err != nil {
		return nil, err
	}
	if args.Offset < 0 {
		return nil, invalid("invalid_argument", "offset must be non-negative")
	}
	apps, err := m.client.ListApps(ctx)
	if err != nil {
		return nil, err
	}
	apps = m.filterApps(apps)
	sort.Slice(apps, func(i, j int) bool { return apps[i].Slug < apps[j].Slug })
	start := min(args.Offset, len(apps))
	end := start + min(limit, len(apps)-start)
	items := make([]appView, 0, end-start)
	for _, a := range apps[start:end] {
		items = append(items, projectApp(a))
	}
	result := map[string]any{"items": items, "total": len(apps)}
	if end < len(apps) {
		result["next_offset"] = end
	}
	return result, nil
}

func (m *management) getApp(ctx context.Context, args appArgs) (any, error) {
	if err := validateSegment(args.App, "app"); err != nil {
		return nil, err
	}
	a, _, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, false)
	return projectApp(a), err
}

type deploymentView struct {
	RolloutState          string                             `json:"rollout_state,omitempty"`
	ServiceRolloutHandoff *api.ServiceRolloutHandoffResponse `json:"service_rollout_handoff,omitempty"`
	ID                    string                             `json:"id"`
	AppID                 string                             `json:"app_id"`
	Revision              int                                `json:"revision,omitempty"`
	BuildID               string                             `json:"build_id,omitempty"`
	Kind                  string                             `json:"kind"`
	Status                string                             `json:"status"`
	CreatedAt             string                             `json:"created_at"`
	ImageDigest           string                             `json:"image_digest,omitempty"`
	CommitSHA             string                             `json:"commit_sha,omitempty"`
	ErrorCode             string                             `json:"error_code,omitempty"`
	Error                 string                             `json:"error,omitempty"`
	ErrorHint             string                             `json:"error_hint,omitempty"`
	ErrorWhy              string                             `json:"error_why,omitempty"`
	ErrorFix              string                             `json:"error_fix,omitempty"`
	Scope                 string                             `json:"scope"`
	TrafficPercent        int                                `json:"traffic_percent"`
}

func projectDeployment(d api.DeploymentResponse) deploymentView {
	return deploymentView{
		RolloutState: d.RolloutState, ServiceRolloutHandoff: d.ServiceRolloutHandoff, ID: d.ID, AppID: d.AppID, Revision: d.Revision, BuildID: d.BuildID, Kind: d.Kind,
		Status: d.Status, CreatedAt: d.CreatedAt, ImageDigest: d.ImageDigest, CommitSHA: d.CommitSHA,
		ErrorCode: d.ErrorCode, Error: d.Error, ErrorHint: d.ErrorHint, ErrorWhy: d.ErrorWhy, ErrorFix: d.ErrorFix,
		Scope: d.Scope, TrafficPercent: d.TrafficPercent,
	}
}

func (m *management) listDeployments(ctx context.Context, args deploymentsArgs) (any, error) {
	limit, err := pageLimit(args.Limit)
	if err != nil {
		return nil, err
	}
	if len(args.Before) > maxDiagnosticBytes {
		return nil, invalid("invalid_argument", "before cursor is too long")
	}
	page, err := m.client.ListDeployments(ctx, args.Before, limit)
	if err != nil {
		return nil, err
	}
	if len(page.Items) > limit {
		return nil, invalid("invalid_response", "API returned more deployments than requested")
	}
	items := make([]deploymentView, 0, len(page.Items))
	for _, d := range page.Items {
		if m.deploymentAccess(ctx, d) == nil {
			items = append(items, projectDeployment(d))
		}
	}
	return map[string]any{"items": items, "next_before": page.NextBefore}, nil
}

func (m *management) getDeployment(ctx context.Context, args deploymentArgs) (any, error) {
	if err := validateSegment(args.DeploymentID, "deployment_id"); err != nil {
		return nil, err
	}
	d, err := m.client.GetDeployment(ctx, args.DeploymentID)
	if err == nil {
		err = m.deploymentAccess(ctx, d)
	}
	return projectDeployment(d), err
}

type metricsArgs struct {
	App         string `json:"app" jsonschema:"Gregale app slug"`
	Project     string `json:"project,omitempty" jsonschema:"Project slug required with a named environment"`
	Environment string `json:"environment,omitempty" jsonschema:"Registered named environment; omit for default"`
	Range       string `json:"range,omitempty" jsonschema:"Metrics window accepted by the Gregale API; default 5m"`
}

func (m *management) getMetrics(ctx context.Context, args metricsArgs) (any, error) {
	if m.opts.Environments != nil || args.Environment != "" {
		if args.Range != "" && args.Range != "5m" {
			return nil, invalid("invalid_argument", "scoped request metrics use the fixed 5m health window")
		}
		health, err := m.scopedHealth(ctx, appArgs{App: args.App, Project: args.Project, Environment: args.Environment})
		if err != nil {
			return nil, err
		}
		source, reason := "unavailable", "request_evidence_unavailable"
		if health.Requests != nil && health.Requests.Known {
			source, reason = "prometheus", ""
		}
		for _, check := range health.Checks {
			if check.Code == "requests" {
				reason = check.Reason
			}
		}
		return map[string]any{"app_id": health.AppID, "scope": health.Scope, "environment_id": health.EnvironmentID, "range": "5m", "source": source, "reason": reason, "as_of": health.MetricsAsOf, "requests": health.Requests}, nil
	}
	if err := validateSegment(args.App, "app"); err != nil {
		return nil, err
	}
	if len(args.Range) > 32 {
		return nil, invalid("invalid_argument", "range is too long")
	}
	return m.client.GetAppMetrics(ctx, args.App, args.Range)
}
