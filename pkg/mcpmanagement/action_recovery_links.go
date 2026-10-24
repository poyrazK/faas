package mcpmanagement

import "time"

type savedPromptReference struct {
	Name      string `json:"name"`
	Arguments any    `json:"arguments"`
}

// Discovery metadata is read-only guidance, never apply or retry authority.
type actionRecoveryLinks struct {
	ResourceURI string               `json:"resource_uri"`
	Prompt      savedPromptReference `json:"prompt"`
	Check       savedPlanReference   `json:"check"`
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
	if view.PlanAvailable && !m.actionPlanAvailable(view.actionRecord, now) {
		return m.actionView(view.actionRecord, now)
	}
	return view
}

func (m *management) currentEnvironmentRecoveryLinks(view environmentActionView, now time.Time) environmentActionView {
	if view.PlanAvailable && !m.environmentActionPlanAvailable(view.environmentActionRecord, now) {
		return m.environmentActionView(view.environmentActionRecord, now)
	}
	return view
}
