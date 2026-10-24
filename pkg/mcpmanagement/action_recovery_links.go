package mcpmanagement

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type savedPromptReference struct {
	Name      string `json:"name"`
	Arguments any    `json:"arguments"`
}

// Discovery metadata is read-only guidance, never apply or retry authority.
type actionRecoveryLinks struct {
	ResourceURI string               `json:"resource_uri"`
	Prompt      savedPromptReference `json:"prompt"`
	Check       savedPlanReference   `json:"check,omitzero"`
	Outcome     *savedPlanReference  `json:"outcome,omitempty"`
}

func (m *management) recoveryLinks(args getActionArgs, environment bool) *actionRecoveryLinks {
	if m.validateSavedRecoveryTarget(args, environment) != nil {
		return nil
	}
	var selector any = args
	prompt, check := "gregale_saved_plan_review", "gregale_check_plan"
	if environment {
		selector = getEnvironmentActionArgs{Project: args.Project, Environment: args.Environment, PlanID: args.PlanID}
		prompt, check = "gregale_environment_plan_review", "gregale_check_environment_plan"
	}
	return &actionRecoveryLinks{ResourceURI: savedPlanRecoveryURI(args, environment), Prompt: savedPromptReference{Name: prompt, Arguments: selector}, Check: savedPlanReference{Tool: check, Arguments: selector}}
}

// Recheck only discovery availability; keep recorded phases, receipts, ordering
// and cursor metadata at their original observation. Never upgrade an unavailable
// snapshot or persist a new baseline/expiry while releasing retained history.
func (m *management) refreshHistoryRecoveryLinks(read *guardedRead) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.opts.planStore.available() {
		return invalid("state_unavailable", "action history storage is unavailable; restore storage and restart")
	}
	now := time.Now().UTC()
	switch value := read.value.(type) {
	case actionHistoryDetail:
		value.Action = m.currentAppRecoveryLinks(value.Action, now)
		read.value = value
	case actionHistoryPage:
		rows := make([]actionView, len(value.Items))
		copy(rows, value.Items)
		for i := range rows {
			rows[i] = m.currentAppRecoveryLinks(rows[i], now)
		}
		value.Items = rows
		read.value = value
	case environmentActionDetail:
		value.Action = m.currentEnvironmentRecoveryLinks(value.Action, now)
		read.value = value
	case environmentActionPage:
		rows := make([]environmentActionView, len(value.Items))
		copy(rows, value.Items)
		for i := range rows {
			rows[i] = m.currentEnvironmentRecoveryLinks(rows[i], now)
		}
		value.Items = rows
		read.value = value
	}
	return nil
}

func (m *management) currentAppRecoveryLinks(view actionView, now time.Time) actionView {
	hadOutcome := view.Recovery != nil && view.Recovery.Outcome != nil
	if view.PlanAvailable && !m.actionPlanAvailable(view.actionRecord, now) {
		view = m.actionView(view.actionRecord, now)
	}
	if view.Recovery != nil {
		links := *view.Recovery
		if !hadOutcome || m.outcomeRecoveryReference(view.actionRecord, now) == nil {
			links.Outcome = nil
		}
		view.Recovery = &links
		if !view.PlanAvailable && links.Outcome == nil {
			view.Recovery = nil
		}
	}
	return view
}

// Called under m.mu. Eligibility concerns a retained read-only reference, never
// availability of the original mutation body or permission to replay it.
func (m *management) outcomeRecoveryReference(entry actionRecord, now time.Time) *savedPlanReference {
	current, ok := m.actions[entry.PlanID]
	lookup, found := m.budgetOutcomes[entry.PlanID]
	if !ok || !found || entry.Action != "route_budgets" || entry.Apply.State == "not_attempted" || !now.Before(entry.RetainedUntil) || !now.Before(current.RetainedUntil) || budgetOutcomeBinding(current) != budgetOutcomeBinding(entry) || !validBudgetOutcomeLookup(lookup) || lookup.Binding != budgetOutcomeBinding(entry) {
		return nil
	}
	environment := entry.Scope
	if environment == api.DefaultEnvScope {
		environment = ""
	}
	args := getActionArgs{App: entry.App, Project: entry.Project, Environment: environment, PlanID: entry.PlanID}
	if m.validateSavedRecoveryTarget(args, false) != nil {
		return nil
	}
	return &savedPlanReference{Tool: "gregale_get_action_outcome", Arguments: args}
}

func (m *management) addOutcomeRecoveryLinks(view actionView, now time.Time) actionView {
	call := m.outcomeRecoveryReference(view.actionRecord, now)
	if call == nil {
		return view
	}
	if view.Recovery == nil {
		args := call.Arguments.(getActionArgs)
		view.Recovery = &actionRecoveryLinks{ResourceURI: savedPlanRecoveryURI(args, false), Prompt: savedPromptReference{Name: "gregale_saved_plan_review", Arguments: args}}
	}
	view.Recovery.Outcome = call
	return view
}

func (m *management) currentEnvironmentRecoveryLinks(view environmentActionView, now time.Time) environmentActionView {
	if view.PlanAvailable && !m.environmentActionPlanAvailable(view.environmentActionRecord, now) {
		return m.environmentActionView(view.environmentActionRecord, now)
	}
	return view
}
