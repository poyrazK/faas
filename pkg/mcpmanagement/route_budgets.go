package mcpmanagement

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routemonitor"
)

type budgetPlanArgs struct {
	App         string                  `json:"app" jsonschema:"Gregale app slug"`
	Project     string                  `json:"project,omitempty" jsonschema:"Project slug required for a named environment"`
	Environment string                  `json:"environment,omitempty" jsonschema:"Registered named environment; omit for default"`
	Reason      string                  `json:"reason" jsonschema:"Reviewable reason, at most 280 characters"`
	Inherit     bool                    `json:"inherit" jsonschema:"Restore app-budget inheritance; requires enabled=false and empty routes"`
	Enabled     bool                    `json:"enabled" jsonschema:"Enable the explicit environment budget override"`
	Routes      []api.RouteMonitorRoute `json:"routes" jsonschema:"Exact normalized routes with error-rate and/or latency budgets; empty when inheriting"`
}

type routeBudgetPlan struct {
	Before   api.EnvironmentRouteBudgets           `json:"before"`
	Proposed api.SetEnvironmentRouteBudgetsRequest `json:"proposed"`
}

func registerRouteBudgets(s *mcp.Server, m *management) {
	register(s, m, "gregale_get_route_budgets", "Read effective advisory route budgets and their app or environment policy source for exactly one consented app/environment.", m.getRouteBudgets)
	register(s, m, "gregale_plan_route_budgets", "Review an advisory environment budget override or return to app inheritance. Pins identities and both policy revisions for 15 minutes. Saving resets observation windows; no canary gate, rollback, incident or traffic action changes.", m.planRouteBudgets)
	if m.opts.AllowWrites {
		registerTool(s, m, "gregale_apply_route_budgets", "Apply a reviewed advisory budget plan with environment write consent and native revision guards. Retry the same plan after an ambiguous response; durable receipts recover the original result. No deployment or traffic changes.", false, m.applyRouteBudgets)
	}
}

func (m *management) getRouteBudgets(ctx context.Context, args appArgs) (any, error) {
	app, env, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, false)
	if err != nil {
		return nil, err
	}
	if !m.appIDAllowed(app.ID) {
		return nil, invalid("app_access_denied", "app identity is outside consent")
	}
	out, err := m.client.GetEnvironmentRouteBudgets(ctx, args.App, args.Environment)
	if err != nil {
		return nil, err
	}
	if err := validateBudgetResponse(out, app.ID, normalizedScope(args.Environment), env.ProjectID, env.ID); err != nil {
		return nil, err
	}
	return out, nil
}

func validateBudgetResponse(out api.EnvironmentRouteBudgets, app, scope, project, environment string) error {
	if out.AppID != app || out.Scope != scope || out.ProjectID != project || out.EnvironmentID != environment || out.Config.AppID != app || out.Config.CustomerGroupBy != "" || out.Config.OnViolation != "" || out.Revision < 0 || out.Revision > api.RouteRequirementsMaxRevision || out.AppRevision < 0 || out.AppRevision > api.RouteRequirementsMaxRevision {
		return invalid("invalid_response", "route budgets lack exact identity or advisory configuration")
	}
	if out.BudgetSource != "app_policy" && out.BudgetSource != "environment_policy" || out.BudgetSource == "environment_policy" && (out.Revision == 0 || out.Config.Revision != out.Revision) || out.BudgetSource == "app_policy" && out.Config.Revision != out.AppRevision {
		return invalid("invalid_response", "route budget policy source and revisions are inconsistent")
	}
	rev := out.Config.Revision
	if routemonitor.Validate(api.SetRouteMonitorRequest{Enabled: out.Config.Enabled, ExpectedRevision: &rev, Routes: out.Config.Routes}) != nil {
		return invalid("invalid_response", "route budget configuration is invalid")
	}
	return nil
}

func (m *management) planRouteBudgets(ctx context.Context, args budgetPlanArgs) (any, error) {
	if err := validateReason(args.App, args.Reason); err != nil {
		return nil, err
	}
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return nil, err
	}
	if account.ID == "" {
		return nil, invalid("invalid_response", "account identity is missing")
	}
	value, err := m.getRouteBudgets(ctx, appArgs{App: args.App, Project: args.Project, Environment: args.Environment})
	if err != nil {
		return nil, err
	}
	before := value.(api.EnvironmentRouteBudgets)
	req := api.SetEnvironmentRouteBudgetsRequest{AppID: before.AppID, ProjectID: before.ProjectID, EnvironmentID: before.EnvironmentID, RequestID: uuid.NewString(), ExpectedRevision: &before.Revision, ExpectedAppRevision: &before.AppRevision, Inherit: args.Inherit, Enabled: args.Enabled, Routes: routemonitor.CloneRoutes(args.Routes)}
	if err := routemonitor.ValidateEnvironmentBudgets(req); err != nil {
		return nil, invalid("invalid_argument", err.Error())
	}
	p := &releasePlan{Action: "route_budgets", AccountID: account.ID, APIBase: m.opts.APIBase, Profile: m.opts.Profile, AppID: before.AppID, App: args.App, Scope: before.Scope, Project: args.Project, ProjectID: before.ProjectID, EnvironmentID: before.EnvironmentID, Reason: args.Reason, TrafficEffect: "No traffic changes; advisory policy only. A save requires fresh completed observation windows.", ExpiresAt: time.Now().Add(releasePlanTTL), Budgets: &routeBudgetPlan{Before: before, Proposed: req}}
	return m.savePlan(p)
}

func (m *management) applyRouteBudgets(ctx context.Context, args applyArgs) (any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.checkedPlan(ctx, args.PlanID, "route_budgets")
	if err != nil {
		return nil, err
	}
	if p.Budgets == nil {
		return nil, invalid("invalid_plan", "route budget review is missing")
	}
	if p.budgetResult != nil {
		return *p.budgetResult, nil
	}
	p.attempted = true
	if err := m.persistPlans(); err != nil {
		return nil, err
	}
	environment := ""
	if p.Scope != api.DefaultEnvScope {
		environment = p.Scope
	}
	out, err := m.client.SetEnvironmentRouteBudgets(ctx, p.App, environment, p.Budgets.Proposed)
	if err != nil {
		return nil, err
	}
	if err := validateBudgetReceipt(p, out); err != nil {
		return nil, err
	}
	p.budgetResult = &out
	if err := m.persistPlans(); err != nil {
		return nil, err
	}
	return out, nil
}

func validateBudgetReceipt(p *releasePlan, out api.EnvironmentRouteBudgets) error {
	if p.Budgets == nil || p.Budgets.Proposed.ExpectedRevision == nil || p.Budgets.Proposed.ExpectedAppRevision == nil {
		return invalid("invalid_response", "route budget review lacks policy revisions")
	}
	if err := validateBudgetResponse(out, p.AppID, p.Scope, p.ProjectID, p.EnvironmentID); err != nil {
		return err
	}
	req := p.Budgets.Proposed
	if out.Revision != *req.ExpectedRevision+1 || out.AppRevision != *req.ExpectedAppRevision || req.Inherit && out.BudgetSource != "app_policy" || !req.Inherit && (out.BudgetSource != "environment_policy" || out.Config.Enabled != req.Enabled || !routemonitor.RoutesEqual(out.Config.Routes, req.Routes)) {
		return invalid("invalid_response", "saved route budgets differ from reviewed proposal")
	}
	return nil
}
