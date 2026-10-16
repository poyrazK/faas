package mcpmanagement

import (
	"context"
	"sort"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routemonitor"
)

func registerInspection(s *mcp.Server, m *management) {
	register(s, m, "gregale_list_app_deployments", "List a bounded deployment history for one app. Continue with next_before.", m.listAppDeployments)
	register(s, m, "gregale_list_instances", "Inspect a bounded page of app instances. Host addresses are excluded.", m.listInstances)
	register(s, m, "gregale_get_health", "Read current app health, observation freshness, serving releases, and readiness checks. Build success alone does not establish health.", m.getHealth)
	register(s, m, "gregale_get_routes", "Read observed app routes and telemetry coverage. An unavailable source is not evidence that there are no routes.", m.getRoutes)
	register(s, m, "gregale_get_operational_summary", "Inspect independently observed monitoring and recovery operations, including checked rollback progress.", m.getOperationalSummary)
	register(s, m, "gregale_get_rollback", "Inspect a checked rollback operation's progress and stable blocker codes.", m.getRollback)
}

type appDeploymentsArgs struct {
	App    string `json:"app" jsonschema:"Gregale app slug"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Page size from 1 to 100; default 20"`
	Before string `json:"before,omitempty" jsonschema:"Opaque next_before cursor"`
}

func (m *management) listAppDeployments(ctx context.Context, args appDeploymentsArgs) (any, error) {
	if err := validateSegment(args.App, "app"); err != nil {
		return nil, err
	}
	limit, err := pageLimit(args.Limit)
	if err != nil {
		return nil, err
	}
	if len(args.Before) > maxDiagnosticBytes {
		return nil, invalid("invalid_argument", "before cursor is too long")
	}
	page, err := m.client.ListAppDeployments(ctx, args.App, args.Before, limit)
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

type instancesArgs struct {
	App     string `json:"app" jsonschema:"Gregale app slug"`
	Limit   int    `json:"limit,omitempty" jsonschema:"Page size from 1 to 100; default 20"`
	Offset  int    `json:"offset,omitempty" jsonschema:"Non-negative offset; default 0"`
	History bool   `json:"history,omitempty" jsonschema:"Include historical instances"`
}

type instanceView struct {
	ID           string `json:"id"`
	AppID        string `json:"app_id"`
	DeploymentID string `json:"deployment_id"`
	State        string `json:"state"`
	RAMMB        int    `json:"ram_mb"`
	Resident     bool   `json:"resident"`
	StartedAt    string `json:"started_at,omitempty"`
}

func (m *management) listInstances(ctx context.Context, args instancesArgs) (any, error) {
	if err := validateSegment(args.App, "app"); err != nil {
		return nil, err
	}
	limit, err := pageLimit(args.Limit)
	if err != nil {
		return nil, err
	}
	if args.Offset < 0 {
		return nil, invalid("invalid_argument", "offset must be non-negative")
	}
	rows, err := m.client.ListInstancesWithHistory(ctx, args.App, args.History)
	if err != nil {
		return nil, err
	}
	if m.opts.Environments != nil {
		filtered := rows[:0]
		for _, row := range rows {
			if row.DeploymentID == "" {
				continue
			}
			d, err := m.client.GetDeployment(ctx, row.DeploymentID)
			if err != nil {
				return nil, err
			}
			if m.deploymentAccess(ctx, d) == nil {
				filtered = append(filtered, row)
			}
		}
		rows = filtered
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	start := min(args.Offset, len(rows))
	end := start + min(limit, len(rows)-start)
	items := make([]instanceView, 0, end-start)
	for _, i := range rows[start:end] {
		items = append(items, instanceView{ID: i.ID, AppID: i.AppID, DeploymentID: i.DeploymentID, State: i.State, RAMMB: i.RAMMB, Resident: i.Resident, StartedAt: i.StartedAt})
	}
	out := map[string]any{"items": items, "total": len(rows)}
	if end < len(rows) {
		out["next_offset"] = end
	}
	return out, nil
}

func (m *management) getHealth(ctx context.Context, args appArgs) (any, error) {
	if err := validateSegment(args.App, "app"); err != nil {
		return nil, err
	}
	return m.scopedHealth(ctx, args)
}

func (m *management) getRoutes(ctx context.Context, args appArgs) (any, error) {
	app, env, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, false)
	if err != nil {
		return nil, err
	}
	out, err := m.client.GetScopedAppRoutes(ctx, args.App, args.Environment)
	if err != nil {
		return nil, err
	}
	if out.AppID != app.ID || out.Scope != normalizedScope(args.Environment) || out.EnvironmentID != env.ID || out.ProjectID != env.ProjectID || out.Coverage != "deployment_attributed_history" {
		return nil, invalid("invalid_response", "route response lacks exact environment attribution")
	}
	return out, nil
}
func (m *management) getOperationalSummary(ctx context.Context, args appArgs) (any, error) {
	app, env, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, false)
	if err != nil {
		return nil, err
	}
	out, err := m.client.GetScopedOperationalSummary(ctx, args.App, args.Environment)
	if err != nil {
		return nil, err
	}
	if out.AppID != app.ID || out.Scope != normalizedScope(args.Environment) || out.EnvironmentID != env.ID || out.ProjectID != env.ProjectID || out.Recovery.Coverage != "explicit_scope_only" {
		return nil, invalid("invalid_response", "operational response lacks exact environment attribution")
	}
	for _, r := range out.Recovery.Rollbacks {
		if normalizedScope(r.Scope) != out.Scope {
			return nil, invalid("invalid_response", "recovery response contains another environment")
		}
	}
	if out.Health != nil && (out.Health.AppID != app.ID || out.Health.Scope != out.Scope || out.Health.EnvironmentID != env.ID || out.Health.ProjectID != env.ProjectID) {
		return nil, invalid("invalid_response", "health response contains another environment")
	}
	if out.Monitoring.Available && out.Monitoring.RouteBudgets == nil {
		return nil, invalid("invalid_response", "available scoped monitoring lacks route budget evidence")
	}
	if b := out.Monitoring.RouteBudgets; b != nil {
		if b.Scope != out.Scope || b.EnvironmentID != env.ID || b.ProjectID != env.ProjectID || (b.BudgetSource != "app_policy" && b.BudgetSource != "environment_policy") || (b.BudgetSource == "environment_policy" && (b.BudgetRevision <= 0 || b.Report.Revision != b.BudgetRevision)) || b.Report.AppID != app.ID || b.Report.DeploymentID != out.Monitoring.DeploymentID || b.Report.CustomerGroupBy != "" || b.Report.Customers != nil || routemonitor.ValidateReport(b.Report) != nil || !out.Monitoring.Available || out.Monitoring.Status != b.Report.Status || out.Monitoring.Coverage != "deployment_attributed_current_windows" {
			return nil, invalid("invalid_response", "route budget response contains another environment or customer cohorts")
		}
		if out.Monitoring.CheckedAt == nil || !out.Monitoring.CheckedAt.Equal(b.Report.CheckedAt) || time.Since(b.Report.CheckedAt) > api.AppHealthEvidenceMaxAge || b.Report.CheckedAt.After(time.Now().Add(api.AppHealthEvidenceMaxAge)) {
			return nil, invalid("invalid_response", "scoped route budget report is stale or has inconsistent observation time")
		}
		for _, route := range b.Report.Routes {
			if len(route.PooledWindows) != 0 {
				return nil, invalid("invalid_response", "scoped budget response reused pooled history")
			}
		}
	}
	return out, nil
}

type rollbackArgs struct {
	App         string `json:"app" jsonschema:"Gregale app slug"`
	OperationID string `json:"operation_id" jsonschema:"Checked rollback operation ID"`
}

func (m *management) getRollback(ctx context.Context, args rollbackArgs) (any, error) {
	if err := validateSegment(args.App, "app"); err != nil {
		return nil, err
	}
	if err := validateSegment(args.OperationID, "operation_id"); err != nil {
		return nil, err
	}
	op, err := m.client.GetRollbackOperation(ctx, args.App, args.OperationID)
	if err != nil {
		return nil, err
	}
	if err := m.deploymentAccess(ctx, api.DeploymentResponse{AppID: op.AppID, Scope: op.Scope}); err != nil {
		return nil, err
	}
	blockers := make([]string, 0, len(op.Blockers))
	for _, finding := range op.Blockers {
		if finding.Code != "" {
			blockers = append(blockers, finding.Code)
		}
	}
	return map[string]any{"id": op.ID, "app_id": op.AppID, "scope": op.Scope, "target_deployment_id": op.TargetDeploymentID, "current_deployment_id": op.CurrentDeploymentID, "status": op.Status, "code": op.Code, "blocker_codes": blockers, "service": op.Service, "created_at": op.CreatedAt, "updated_at": op.UpdatedAt, "completed_at": op.CompletedAt}, nil
}
