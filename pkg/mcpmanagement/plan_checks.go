package mcpmanagement

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Advisory observations never reserve authority or persist a new baseline.
type planCheck struct {
	PlanID           string    `json:"plan_id"`
	ObservedAt       time.Time `json:"observed_at"`
	ExpiresAt        time.Time `json:"expires_at"`
	ApplyEligibility string    `json:"apply_eligibility"`
	BaselineCheck    string    `json:"baseline_check"`
	Blockers         []string  `json:"blockers"`
	NativeCompletion string    `json:"native_completion"`
	Instruction      string    `json:"instruction"`
}

func newPlanCheck(id string, expires time.Time) *planCheck {
	return &planCheck{PlanID: id, ObservedAt: time.Now().UTC(), ExpiresAt: expires, ApplyEligibility: "no_observed_blockers", BaselineCheck: "not_checked", Blockers: []string{}, NativeCompletion: "not_observed", Instruction: "Advisory snapshot only. Apply rechecks authority and native guards; success is not guaranteed."}
}

func (v *planCheck) observe(err error) {
	if err == nil {
		return
	}
	var local *inputError
	if errors.As(err, &local) && local.code != "invalid_response" && local.code != "approval_not_checked" {
		v.Blockers = append(v.Blockers, local.code)
		v.ApplyEligibility = "blocked"
		return
	}
	code := "native_state_unavailable"
	if local != nil && local.code == "approval_not_checked" {
		code = local.code
	}
	v.Blockers = append(v.Blockers, code)
	if v.ApplyEligibility != "blocked" {
		v.ApplyEligibility = "unknown"
	}
}

func (v *planCheck) replay(attempted, receipt bool) bool {
	if !attempted && !receipt {
		return false
	}
	v.BaselineCheck = "retry_preserved"
	v.ApplyEligibility = "retry_original_plan"
	v.Instruction = "An earlier attempt may have committed. Inspect native state or retry the original unexpired plan through its guarded apply tool; do not replace its request."
	if receipt {
		v.ApplyEligibility = "receipt_recorded"
		v.Instruction = "This connection recorded a receipt. Inspect native state for completion; the receipt does not prove healthy traffic or recovery."
	}
	return true
}

func (m *management) checkPlan(ctx context.Context, args getActionArgs) (any, error) {
	value, err := m.getPlan(ctx, args)
	if err != nil {
		return nil, err
	}
	guard := value.(*guardedRead)
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.plans[args.PlanID]
	if p == nil {
		return nil, invalid("invalid_plan", "saved plan is no longer available")
	}
	out := newPlanCheck(p.ID, p.ExpiresAt)
	replay := out.replay(p.attempted, p.result != nil || p.budgetResult != nil || p.regressionResult != nil)
	out.observe(m.checkAppPlanAuthority(ctx, p))
	if !replay {
		out.observe(m.checkAppPlanBaseline(ctx, p, out))
	}
	guard.value = out
	return guard, nil
}

func planEnvironment(p *releasePlan) string {
	if p.Scope == api.DefaultEnvScope {
		return ""
	}
	return p.Scope
}

func (m *management) checkAppPlanAuthority(ctx context.Context, p *releasePlan) error {
	if !m.opts.AllowWrites {
		return invalid("writes_disabled", "connection writes are disabled")
	}
	app, env, err := m.appEnvironment(ctx, p.App, p.Project, planEnvironment(p), true)
	if err != nil {
		return err
	}
	if app.ID != p.AppID || env.ID != p.EnvironmentID || env.ProjectID != p.ProjectID {
		return invalid("plan_stale", "target identity changed")
	}
	if env.Protected && (p.Action == "workload_rollout" || p.Action == "service_abort") {
		return invalid("approval_required", "protected workload release requires environment workflow")
	}
	if p.Action == "regression_action" && (!m.requestReadAllowed(p.App, p.AppID) || m.opts.Environments != nil && m.opts.Environments.RegressionWriteApps[p.App] != p.AppID) {
		return invalid("regression_access_denied", "triage write consent is missing")
	}
	return nil
}

func (m *management) checkAppPlanBaseline(ctx context.Context, p *releasePlan, out *planCheck) error {
	var err error
	switch p.Action {
	case "route_budgets":
		err = m.checkBudgetBaseline(ctx, p)
	case "regression_action":
		err = m.checkRegressionBaseline(ctx, p)
	default:
		if p.review == nil {
			return invalid("invalid_plan", "release review is missing")
		}
		err = m.verifyBaseline(ctx, p)
	}
	if err != nil {
		out.BaselineCheck = "not_verified"
		return err
	}
	out.BaselineCheck = "unchanged"
	return nil
}

func (m *management) checkBudgetBaseline(ctx context.Context, p *releasePlan) error {
	if p.Budgets == nil || p.Budgets.Proposed.ExpectedRevision == nil || p.Budgets.Proposed.ExpectedAppRevision == nil {
		return invalid("invalid_plan", "budget revisions are missing")
	}
	value, err := m.getRouteBudgets(ctx, appArgs{App: p.App, Project: p.Project, Environment: planEnvironment(p)})
	if err != nil {
		return err
	}
	current := value.(api.EnvironmentRouteBudgets)
	if current.Revision != *p.Budgets.Proposed.ExpectedRevision || current.AppRevision != *p.Budgets.Proposed.ExpectedAppRevision {
		return invalid("plan_stale", "budget revisions changed")
	}
	return nil
}

func (m *management) checkRegressionBaseline(ctx context.Context, p *releasePlan) error {
	if err := validateRegressionPlan(p); err != nil {
		return err
	}
	r := p.Regression.Proposed
	dep, err := m.client.GetDeployment(ctx, r.DeploymentID)
	if err != nil {
		return err
	}
	if dep.ID != r.DeploymentID || dep.AppID != p.AppID || normalizedScope(dep.Scope) != p.Scope {
		return invalid("plan_stale", "regression deployment changed")
	}
	current, err := m.client.GetAppDebugRegressionReview(ctx, p.App, r.DeploymentID, r.Route)
	if err != nil {
		return err
	}
	if err := validateRegressionReview(current, p.AppID, r.DeploymentID, r.Route); err != nil {
		return err
	}
	if current.Version != r.ExpectedVersion {
		return invalid("plan_stale", "regression observation changed")
	}
	if r.DismissedUntil != "" {
		until, err := time.Parse(time.RFC3339Nano, r.DismissedUntil)
		if err != nil || !time.Now().Before(until) {
			return invalid("dismissal_expired", "original dismissal expiry passed")
		}
	}
	return nil
}

func (m *management) checkEnvironmentPlan(ctx context.Context, args getEnvironmentActionArgs) (any, error) {
	value, err := m.getEnvironmentPlan(ctx, args)
	if err != nil {
		return nil, err
	}
	guard := value.(*guardedRead)
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.environmentPlans[args.PlanID]
	if p == nil {
		return nil, invalid("invalid_plan", "saved environment plan is no longer available")
	}
	out := newPlanCheck(p.ID, p.ExpiresAt)
	replay := out.replay(p.attempted, p.result != nil)
	_, err = m.checkedEnvironmentPlan(ctx, p.ID, p.Action)
	out.observe(err)
	if !replay {
		out.observe(m.checkEnvironmentBaseline(ctx, p, out))
	}
	guard.value = out
	return guard, nil
}

func (m *management) checkEnvironmentBaseline(ctx context.Context, p *environmentPlan, out *planCheck) error {
	if p.Action == "environment_rollback" {
		value, err := m.getEnvironmentPromotion(ctx, environmentPromotionArgs{Project: p.Project, Environment: p.Environment, PromotionID: p.PromotionID})
		if err != nil {
			return err
		}
		status := value.(api.ProjectEnvironmentPromotionStatusResponse)
		if status.PromotionHash != p.PromotionHash || status.FromEnvironment != p.FromEnvironment || status.Status == "running" || status.RollbackStatus != "" {
			return invalid("plan_stale", "promotion is no longer a rollback candidate")
		}
		out.BaselineCheck = "partial_native_rollback_guard_required"
		return nil
	}
	preview, err := m.client.GetProjectEnvironmentPromotionPreview(ctx, p.Project, p.Environment, p.FromEnvironment)
	if err != nil {
		return err
	}
	if !preview.CanPromote || preview.SyncConfig || preview.PromotionHash != p.PromotionHash || preview.ProjectSlug != p.Project || preview.FromEnvironment != p.FromEnvironment || preview.ToEnvironment != p.Environment {
		return invalid("plan_stale", "environment preview changed or promotion is blocked")
	}
	out.BaselineCheck = "unchanged"
	// Never poll browser reviews or inspect/return private approval credentials.
	if p.ApprovalRequired || preview.ApprovalRequired {
		return invalid("approval_not_checked", "native approval remains required and was not checked")
	}
	return nil
}
