package mcpmanagement

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

type regressionActionArgs struct {
	App            string `json:"app" jsonschema:"Existing app with diagnostic read consent"`
	Project        string `json:"project,omitempty" jsonschema:"Project slug required for a named environment"`
	Environment    string `json:"environment,omitempty" jsonschema:"Exact deployment environment; omit for default"`
	DeploymentID   string `json:"deployment_id" jsonschema:"Exact retained regression deployment UUID"`
	Route          string `json:"route" jsonschema:"Exact unmodified native route label without query or fragment"`
	Action         string `json:"action" jsonschema:"acknowledge, dismiss, resolve, or reopen"`
	DismissedUntil string `json:"dismissed_until,omitempty" jsonschema:"Absolute RFC3339 dismissal expiry; dismiss only; defaults to 24h and maximum 30d"`
	Reason         string `json:"reason" jsonschema:"Reviewable triage reason, 1 to 280 characters"`
}

type regressionActionPlan struct {
	Before   api.DebugRegressionReview               `json:"before"`
	Proposed api.GuardedDebugRegressionActionRequest `json:"proposed"`
}

func registerRegressionActions(s *mcp.Server, m *management) {
	register(s, m, "gregale_plan_regression_action", "Review an exact regression acknowledge, dismiss, resolve or reopen action. Pins immutable app/deployment/environment and native observation version for 15 minutes; planning performs no writes. Diagnostic consent and an exact unmodified route are required.", m.planRegressionAction)
	if m.opts.AllowWrites {
		registerTool(s, m, "gregale_apply_regression_action", "Apply reviewed triage metadata with explicit per-app triage and environment write consent. Native observation-version checks reject changed evidence. Retry the same plan after ambiguous responses; durable receipts survive restart. This does not verify recovery or change traffic.", false, m.applyRegressionAction)
	}
}

func selectedRegressionWriteApps(req *http.Request, reads map[string]string, writes bool) (map[string]string, error) {
	selected := req.PostForm["regression_write_apps"]
	if len(selected) > maxOAuthEntries {
		return nil, errors.New("too many triage selections")
	}
	out := map[string]string{}
	for _, slug := range selected {
		if !writes || reads[slug] == "" {
			return nil, errors.New("triage requires write scope and diagnostic read consent")
		}
		out[slug] = reads[slug]
	}
	return out, nil
}

func validateRegressionSelection(deployment, route string) error {
	id, err := uuid.Parse(deployment)
	if err != nil || id == uuid.Nil || id.String() != deployment {
		return invalid("invalid_argument", "deployment_id must be a canonical nonzero UUID")
	}
	if len(route) == 0 || len(route) > api.MCPRequestFieldMaxBytes || strings.TrimSpace(route) != route || !utf8.ValidString(route) || strings.ContainsAny(route, "?#") || strings.ContainsFunc(route, unicode.IsControl) {
		return invalid("invalid_argument", "route must be an exact unmodified UTF-8 label, at most 256 bytes, without query, fragment or controls")
	}
	return nil
}

func validateRegressionReview(v api.DebugRegressionReview, app, dep, route string) error {
	version, err := uuid.Parse(v.Version)
	if err != nil || version == uuid.Nil || version.String() != v.Version || v.AppID != app || v.DeploymentID != dep || v.Route != route {
		return invalid("invalid_response", "native regression review lacks exact app, deployment, route or observation version")
	}
	if v.State != "active" && v.State != "acknowledged" && v.State != "dismissed" && v.State != "resolved" {
		return invalid("invalid_response", "native regression state is unknown")
	}
	for _, stamp := range []string{v.FirstDetectedAt, v.LastDetectedAt} {
		if _, err := time.Parse(time.RFC3339Nano, stamp); err != nil {
			return invalid("invalid_response", "native regression detection timestamp is invalid")
		}
	}
	for _, stamp := range []string{v.AcknowledgedAt, v.DismissedUntil, v.ResolvedAt} {
		if stamp != "" {
			if _, err := time.Parse(time.RFC3339Nano, stamp); err != nil {
				return invalid("invalid_response", "native regression triage timestamp is invalid")
			}
		}
	}
	return nil
}

func (m *management) planRegressionAction(ctx context.Context, args regressionActionArgs) (any, error) {
	if strings.TrimSpace(args.Reason) == "" {
		return nil, invalid("invalid_argument", "reason must not be blank")
	}
	if err := validateReason(args.App, args.Reason); err != nil {
		return nil, err
	}
	if err := validateRegressionSelection(args.DeploymentID, args.Route); err != nil {
		return nil, err
	}
	if !api.AllowedDebugRegressionAction(args.Action) {
		return nil, invalid("invalid_argument", "unknown regression action")
	}
	if m.clean(args.Route) != args.Route {
		return nil, invalid("invalid_argument", "route cannot be safely displayed unchanged")
	}
	now := time.Now().UTC()
	req := api.GuardedDebugRegressionActionRequest{DeploymentID: args.DeploymentID, Route: args.Route, Action: args.Action, Reason: args.Reason}
	if args.Action == "dismiss" {
		until := now.Add(api.DebugRegressionDefaultDismissal)
		if args.DismissedUntil != "" {
			var err error
			until, err = time.Parse(time.RFC3339Nano, args.DismissedUntil)
			if err != nil {
				return nil, invalid("invalid_argument", "invalid dismissal expiry")
			}
		}
		if !until.After(now) || until.After(now.Add(api.DebugRegressionMaxDismissal)) {
			return nil, invalid("invalid_argument", "dismissal expiry must be in the future and at most 30 days away")
		}
		req.DismissedUntil = until.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano)
	} else if args.DismissedUntil != "" {
		return nil, invalid("invalid_argument", "dismissed_until is only valid for dismiss")
	}
	app, err := m.requestApp(ctx, args.App)
	if err != nil {
		return nil, err
	}
	scoped, env, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, false)
	if err != nil {
		return nil, err
	}
	if scoped.ID != app.ID {
		return nil, invalid("plan_stale", "app identity changed during review")
	}
	dep, err := m.client.GetDeployment(ctx, args.DeploymentID)
	if err != nil {
		return nil, err
	}
	if dep.ID != args.DeploymentID || dep.AppID != app.ID || normalizedScope(dep.Scope) != normalizedScope(args.Environment) {
		return nil, invalid("environment_access_denied", "regression deployment does not match the exact selected app and environment")
	}
	before, err := m.client.GetAppDebugRegressionReview(ctx, args.App, args.DeploymentID, args.Route)
	if err != nil {
		return nil, err
	}
	if err := validateRegressionReview(before, app.ID, args.DeploymentID, args.Route); err != nil {
		return nil, err
	}
	fresh, err := m.requestApp(ctx, args.App)
	if err != nil {
		return nil, err
	}
	if fresh.ID != app.ID {
		return nil, invalid("plan_stale", "app identity changed during review")
	}
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return nil, err
	}
	if account.ID == "" {
		return nil, invalid("invalid_response", "missing account identity")
	}
	req.ExpectedAppID, req.ExpectedVersion = app.ID, before.Version
	p := &releasePlan{Action: "regression_action", AccountID: account.ID, App: args.App, AppID: app.ID, APIBase: m.opts.APIBase, Profile: m.opts.Profile, Scope: normalizedScope(args.Environment), Project: args.Project, ProjectID: env.ProjectID, EnvironmentID: env.ID, Reason: args.Reason, TrafficEffect: "Triage metadata only; no deployment, traffic or health changes.", ExpiresAt: now.Add(releasePlanTTL), Regression: &regressionActionPlan{Before: before, Proposed: req}}
	return m.savePlan(p)
}

func validateRegressionPlan(p *releasePlan) error {
	if p.Regression == nil {
		return invalid("invalid_plan", "regression review is missing")
	}
	r := p.Regression
	if strings.TrimSpace(p.Reason) == "" || validateReason(p.App, p.Reason) != nil || validateRegressionSelection(r.Proposed.DeploymentID, r.Proposed.Route) != nil || !api.AllowedDebugRegressionAction(r.Proposed.Action) || r.Proposed.ExpectedAppID != p.AppID || r.Proposed.ExpectedVersion != r.Before.Version || r.Proposed.Reason != p.Reason {
		return invalid("invalid_plan", "regression proposal binding is invalid")
	}
	if (r.Proposed.Action == "dismiss") != (r.Proposed.DismissedUntil != "") {
		return invalid("invalid_plan", "regression dismissal binding is invalid")
	}
	return validateRegressionReview(r.Before, p.AppID, r.Proposed.DeploymentID, r.Proposed.Route)
}

func validateRegressionReceipt(p *regressionActionPlan, v api.DebugRegressionReview) error {
	if err := validateRegressionReview(v, p.Before.AppID, p.Before.DeploymentID, p.Before.Route); err != nil {
		return err
	}
	state := map[string]string{"acknowledge": "acknowledged", "dismiss": "dismissed", "resolve": "resolved", "reopen": "active"}[p.Proposed.Action]
	if v.Version == p.Before.Version || v.State != state || v.DismissedUntil != p.Proposed.DismissedUntil {
		return invalid("invalid_response", "native triage receipt differs from reviewed action")
	}
	return nil
}

func (m *management) applyRegressionAction(ctx context.Context, args applyArgs) (any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.checkedPlan(ctx, args.PlanID, "regression_action")
	if err != nil {
		return nil, err
	}
	if err := validateRegressionPlan(p); err != nil {
		return nil, err
	}
	if !m.requestReadAllowed(p.App, p.AppID) || m.opts.Environments != nil && m.opts.Environments.RegressionWriteApps[p.App] != p.AppID {
		return nil, invalid("regression_access_denied", "regression triage requires explicit per-app diagnostic and triage consent")
	}
	dep, err := m.client.GetDeployment(ctx, p.Regression.Before.DeploymentID)
	if err != nil {
		return nil, err
	}
	if dep.ID != p.Regression.Before.DeploymentID || dep.AppID != p.AppID || normalizedScope(dep.Scope) != p.Scope {
		return nil, invalid("plan_stale", "regression deployment identity or environment changed")
	}
	if p.regressionResult != nil {
		return *p.regressionResult, nil
	}
	p.attempted = true
	if err := m.persistPlans(); err != nil {
		return nil, err
	}
	out, err := m.client.ApplyAppDebugRegressionAction(api.ContextWithIdempotencyKey(ctx, "mcp-regression-"+p.ID), p.App, p.Regression.Proposed)
	if err != nil {
		return nil, err
	}
	if out.Review == nil {
		return nil, invalid("invalid_response", "native action omitted guarded receipt")
	}
	if err := validateRegressionReceipt(p.Regression, *out.Review); err != nil {
		return nil, err
	}
	p.regressionResult = out.Review
	if err := m.persistPlans(); err != nil {
		return nil, err
	}
	return *out.Review, nil
}
