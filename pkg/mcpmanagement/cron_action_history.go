// adr: 20261105161908
package mcpmanagement

import (
	"encoding/json"
	"errors"
	"reflect"

	"github.com/onebox-faas/faas/pkg/api"
)

func cronActionPhase(p *releasePlan) (*actionCron, actionPhase) {
	phase := actionPhase{State: "not_attempted"}
	if p.Operation == nil || validateSegment(p.Operation.ResourceID, "cron_id") != nil {
		return nil, phase
	}
	cron := &actionCron{ID: p.Operation.ResourceID}
	if !p.attempted {
		return cron, phase
	}
	phase.State = "outcome_unknown"
	// Old bridges could report a missing cron as deleted without making a write.
	// Only plans created with the fail-closed path can establish acceptance.
	if p.localReceipt == nil || !p.Operation.CronHistorySafe {
		return cron, phase
	}
	var local struct {
		PlanID string          `json:"plan_id"`
		App    string          `json:"app"`
		Scope  string          `json:"scope"`
		Kind   string          `json:"kind"`
		Result json.RawMessage `json:"result"`
	}
	raw, err := json.Marshal(p.localReceipt)
	if err != nil || json.Unmarshal(raw, &local) != nil || local.PlanID != p.ID || local.App != p.App || local.Scope != p.Scope || local.Kind != p.Operation.Kind {
		return cron, phase
	}
	switch p.Operation.Kind {
	case "cron_update":
		var result api.CronResponse
		if json.Unmarshal(local.Result, &result) != nil || result.ID != cron.ID || result.AppID != p.AppID {
			return cron, phase
		}
	case "cron_delete":
		var result struct {
			Deleted bool `json:"deleted"`
		}
		if json.Unmarshal(local.Result, &result) != nil || !result.Deleted {
			return cron, phase
		}
	default:
		return nil, phase
	}
	return cron, actionPhase{State: "accepted", Receipt: &actionReceipt{}}
}

func validateCronAction(entry actionRecord) error {
	if !cronHistoryAction(entry.Action) {
		if entry.Cron != nil {
			return errors.New("unexpected cron history")
		}
		return nil
	}
	if entry.Cron == nil || validateSegment(entry.Cron.ID, "cron_id") != nil || entry.Scope != api.DefaultEnvScope || entry.Promotion != nil || entry.Config != nil || entry.Workload != nil || entry.Budget != nil {
		return errors.New("invalid cron history")
	}
	if r := entry.Apply.Receipt; r != nil && (entry.Apply.State != "accepted" || !reflect.DeepEqual(r, &actionReceipt{})) {
		return errors.New("invalid cron receipt")
	}
	return nil
}
