package mcpmanagement

import (
	"context"
	"sort"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type planLookupArgs struct {
	PlanID string `json:"plan_id" jsonschema:"Plan ID previously returned by this connection"`
}

type planListArgs struct {
	Limit  int `json:"limit,omitempty" jsonschema:"1 to 100; default 20"`
	Offset int `json:"offset,omitempty" jsonschema:"Non-negative offset; preserve next_offset"`
}

type planReceipt struct {
	SchemaVersion int           `json:"schema_version"`
	PlanID        string        `json:"plan_id"`
	App           string        `json:"app,omitempty"`
	Project       string        `json:"project,omitempty"`
	Action        string        `json:"action"`
	Scope         string        `json:"scope"`
	Status        string        `json:"status"`
	ExpiresAt     time.Time     `json:"expires_at"`
	Result        any           `json:"result,omitempty"`
	Problem       *ToolError    `json:"problem,omitempty"`
	Recovery      Recovery      `json:"recovery"`
	Verification  *Verification `json:"verification,omitempty"`
}

func registerPlanReceipts(s *mcp.Server, m *management) {
	register(s, m, "gregale_verify_plan", "Verify an applied plan using fresh passive reads of its exact restart or deployment. Separates mutation completion from runtime readiness. Never repeats a mutation, wakes an app or promotes a staged deployment. Reuse the same plan_id across reconnects.", m.verifyPlan)
	register(s, m, "gregale_get_plan", "Recover a plan's status and completed receipt without repeating a mutation. Temporary source files and configuration values are excluded.", m.getPlan)
	register(s, m, "gregale_list_plans", "List this connection's unexpired plans, newest expiration first. Results contain no source paths or configuration values.", m.listPlans)
}

// Caller holds m.mu when the receipt is read from a live plan.
func (m *management) completedReceipt(ctx context.Context, p *releasePlan) (any, error) {
	if _, err := m.checkedPlan(ctx, p.ID, p.Action); err != nil {
		return nil, err
	}
	return p.localReceipt, nil
}

func receiptOf(p *releasePlan) planReceipt {
	r := planReceipt{SchemaVersion: recoveryContractVersion, PlanID: p.ID, App: p.App, Action: p.Action, Scope: p.Scope, Status: "planned", ExpiresAt: p.ExpiresAt, Recovery: Recovery{Action: "inspect"}}
	switch {
	case p.Provision != nil && p.Provision.Native != nil && p.Provision.Native.CancelRequested && p.localReceipt == nil && p.Provision.Native.Phase != "cancelled":
		r.Status = "cancellation_requested"
	case p.localInFlight:
		r.Status = "running"
	case p.promoted != nil:
		r.Status, r.Result = "promoted", releaseResult(p, p.promoted)
	case p.localReceipt != nil:
		r.Status, r.Result = "completed", p.localReceipt
	case p.result != nil:
		r.Status, r.Result = "accepted", releaseResult(p, p.result)
	case p.budgetResult != nil:
		r.Status, r.Result = "completed", p.budgetResult
	case p.regressionResult != nil:
		r.Status, r.Result = "completed", p.regressionResult
	case p.failure != nil:
		r.Status, r.Problem = p.failure.Status, &p.failure.Problem
		if p.configReceipt != nil {
			r.Result = map[string]any{"configuration": p.configReceipt}
		}
	case p.configReceipt != nil:
		r.Status = "partial"
		r.Result = map[string]any{"configuration": p.configReceipt}
	case p.Provision != nil && p.Provision.Native != nil && p.attempted:
		r.Status = "interrupted"
		if p.Provision.Native.Phase == "cancelled" {
			r.Status = "cancelled"
		}
	case p.attempted:
		r.Status = "outcome_unknown"
	}
	return r
}

func (m *management) getPlan(ctx context.Context, args planLookupArgs) (any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.plans[args.PlanID]
	if p == nil {
		if e := m.environmentPlans[args.PlanID]; e != nil && time.Now().Before(e.ExpiresAt) {
			return m.environmentPlanReceipt(ctx, e)
		}
	}
	if p == nil || time.Now().After(p.ExpiresAt) {
		return nil, invalid("invalid_plan", "plan is unknown or expired; inspect deployments and resources before planning a new mutation")
	}
	if err := m.authorizeArgs(ctx, appArgs{App: p.App, Project: p.Project, Environment: namedScope(p.Scope)}); err != nil {
		return nil, err
	}
	return m.recoverableReceipt(p), nil
}

func (m *management) listPlans(ctx context.Context, args planListArgs) (any, error) {
	if _, err := pageLimit(args.Limit); err != nil {
		return nil, err
	}
	if args.Offset < 0 {
		return nil, invalid("invalid_argument", "offset must be non-negative")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	items := []planReceipt{}
	for _, p := range m.plans {
		if time.Now().After(p.ExpiresAt) {
			continue
		}
		if err := m.authorizeArgs(ctx, appArgs{App: p.App, Project: p.Project, Environment: namedScope(p.Scope)}); err != nil {
			continue
		}
		r := m.recoverableReceipt(p)
		r.Result = nil
		items = append(items, r)
	}
	for _, p := range m.environmentPlans {
		if time.Now().After(p.ExpiresAt) {
			continue
		}
		r, err := m.environmentPlanReceipt(ctx, p)
		if err != nil {
			continue
		}
		r.Result = nil
		items = append(items, r)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].ExpiresAt.Equal(items[j].ExpiresAt) {
			return items[i].PlanID < items[j].PlanID
		}
		return items[i].ExpiresAt.After(items[j].ExpiresAt)
	})
	return resourcePage(items, resourcePageArgs{Limit: args.Limit, Offset: args.Offset})
}

func (m *management) environmentPlanReceipt(ctx context.Context, p *environmentPlan) (planReceipt, error) {
	r := planReceipt{SchemaVersion: recoveryContractVersion, PlanID: p.ID, Project: p.Project, Action: p.Action, Scope: p.Environment, Status: "planned", ExpiresAt: p.ExpiresAt, Recovery: Recovery{Action: "inspect"}}
	r.Verification = &Verification{Status: "unknown", Goal: "manual_review", Checks: []verificationCheck{}}
	project, source, err := m.environmentAccess(ctx, p.Project, p.FromEnvironment, false, true)
	if err != nil {
		return r, err
	}
	_, target, err := m.environmentAccess(ctx, p.Project, p.Environment, false, true)
	if err != nil {
		return r, err
	}
	if project.ID != p.ProjectID || source.ID != p.FromID || target.ID != p.EnvironmentID {
		return r, invalid("plan_stale", "environment identity changed since planning")
	}
	switch {
	case p.result != nil:
		r.Status, r.Result = "accepted", p.result
	case p.attempted:
		r.Status = "outcome_unknown"
	case p.approvalRequest != nil:
		r.Status = "awaiting_approval"
	}
	return r, nil
}

func namedScope(scope string) string {
	if scope == "" || scope == "default" {
		return ""
	}
	return scope
}

// Journal receipts are already bounded by the connection's retained plan
// limit. Resource inventories use native paging instead.
func resourcePage[T any](items []T, a resourcePageArgs) (any, error) {
	limit, err := pageLimit(a.Limit)
	if err != nil {
		return nil, err
	}
	if a.Offset < 0 {
		return nil, invalid("invalid_argument", "offset must be non-negative")
	}
	start := min(a.Offset, len(items))
	end := min(start+limit, len(items))
	out := map[string]any{"items": items[start:end], "total": len(items)}
	if end < len(items) {
		out["next_offset"] = end
	}
	return out, nil
}
