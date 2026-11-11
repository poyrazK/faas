package mcpmanagement

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

// planApply is a tool argument that applies a saved plan.
type planApply interface{ appliedPlanID() string }

func (a applyArgs) appliedPlanID() string                     { return a.PlanID }
func (a applyEnvironmentPromotionArgs) appliedPlanID() string { return a.PlanID }

// withPlanReason sends the reviewed plan's reason as Gregale-Agent-Reason on
// the requests that apply it (ADR-20261110182236), so the person deciding an
// approval sees why without a new tool argument.
func (m *management) withPlanReason(ctx context.Context, args any) context.Context {
	apply, ok := args.(planApply)
	if !ok {
		return ctx
	}
	id := apply.appliedPlanID()
	m.mu.Lock()
	reason := ""
	if p := m.plans[id]; p != nil {
		reason = p.Reason
	} else if p := m.environmentPlans[id]; p != nil {
		reason = p.Reason
	}
	m.mu.Unlock()
	if reason == "" {
		return ctx
	}
	return api.WithAgentReason(ctx, reason)
}
