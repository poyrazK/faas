// adr: 20261105152500
package mcpmanagement

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const configRollbackActionsNamespace = "config-rollback-actions/"
const configRollbackOutcomeNamespace = "config-rollback-outcome-lookups/"

type configRollbackLookup struct {
	RequestID, ReceiptProof, Binding string
	ExpectedRevision, TargetRevision int64
}
type configRollbackSnapshot struct {
	Version int
	Items   map[string]configRollbackLookup
}

func configRollbackRequestID(p *releasePlan) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale-mcp-config-rollback:"+p.ID)).String()
}
func configRollbackActionPhase(p *releasePlan) actionPhase {
	phase := actionPhase{State: "not_attempted"}
	if !p.attempted {
		return phase
	}
	phase.State = "outcome_unknown"
	if p.localReceipt == nil || p.Operation == nil {
		return phase
	}
	var target int64
	var local struct {
		Kind   string                     `json:"kind"`
		Result api.ConfigRollbackResponse `json:"result"`
	}
	data, err := json.Marshal(p.localReceipt)
	if err != nil || json.Unmarshal(data, &local) != nil || json.Unmarshal(p.Operation.Request, &target) != nil || local.Kind != "config_rollback_guarded" || local.Result.RestoredRevision != target {
		return phase
	}
	revision := p.Operation.ExpectedRevision
	if local.Result.Change != nil {
		revision = local.Result.Change.Revision
		if revision != p.Operation.ExpectedRevision+1 {
			return phase
		}
	}
	if revision < 1 {
		return phase
	}
	return actionPhase{State: "committed", Receipt: &actionReceipt{ConfigRevision: &revision}}
}
func validConfigRollbackLookup(ref configRollbackLookup) bool {
	id, err := uuid.Parse(ref.RequestID)
	return err == nil && id != uuid.Nil && id.String() == ref.RequestID && api.ValidEnvBatchReceiptProof(ref.ReceiptProof) && ref.ExpectedRevision >= 0 && ref.TargetRevision > 0 && validOutcomeHash(ref.Binding)
}
func validateConfigRollbackAction(entry actionRecord) error {
	if entry.Action != "operation_config_rollback_guarded" {
		return nil
	}
	if entry.Promotion != nil || entry.Config != nil || entry.Workload != nil || entry.Budget != nil {
		return errors.New("invalid configuration rollback history")
	}
	if r := entry.Apply.Receipt; r != nil && (entry.Apply.State != "committed" || r.ConfigRevision == nil || *r.ConfigRevision < 1 || !reflect.DeepEqual(r, &actionReceipt{ConfigRevision: r.ConfigRevision})) {
		return errors.New("invalid configuration rollback receipt")
	}
	return nil
}
func (m *management) restoreConfigRollbackLookups() error {
	var saved configRollbackSnapshot
	if err := m.opts.planStore.read(configRollbackOutcomeNamespace+m.opts.planNamespace, &saved); err != nil {
		return err
	}
	if saved.Version != 1 && (saved.Version != 0 || len(saved.Items) != 0) || len(saved.Items) > api.ManagementMCPActionHistoryMax {
		return errors.New("invalid configuration rollback snapshot")
	}
	m.configRollbacks = make(map[string]configRollbackLookup)
	for id, ref := range saved.Items {
		if !validActionID(id) || !validConfigRollbackLookup(ref) {
			return errors.New("invalid configuration rollback reference")
		}
		if entry, ok := m.actions[id]; ok && time.Now().Before(entry.RetainedUntil) && (entry.Action != "operation_config_rollback_guarded" || entry.Apply.State == "not_attempted" || ref.Binding != actionOutcomeBinding(entry)) {
			return errors.New("configuration rollback binding mismatch")
		}
		m.configRollbacks[id] = ref
	}
	return nil
}
func (m *management) syncConfigRollbackLookups(now time.Time) error {
	if m.configRollbacks == nil {
		m.configRollbacks = make(map[string]configRollbackLookup)
	}
	for id, p := range m.plans {
		entry, ok := m.actions[id]
		if !ok || p.Action != "operation_config_rollback_guarded" || !p.attempted || !now.Before(entry.RetainedUntil) || p.Operation == nil || !p.Operation.RollbackGuarded {
			continue
		}
		var target int64
		if json.Unmarshal(p.Operation.Request, &target) != nil {
			return invalid("invalid_plan", "invalid rollback target")
		}
		ref := configRollbackLookup{RequestID: configRollbackRequestID(p), ReceiptProof: p.Operation.EnvReceiptProof, ExpectedRevision: p.Operation.ExpectedRevision, TargetRevision: target, Binding: actionOutcomeBinding(entry)}
		if !validConfigRollbackLookup(ref) {
			return invalid("invalid_plan", "invalid rollback outcome reference")
		}
		if old, exists := m.configRollbacks[id]; exists && old != ref {
			return invalid("invalid_plan", "rollback outcome reference changed")
		}
		m.configRollbacks[id] = ref
	}
	for id, ref := range m.configRollbacks {
		entry, ok := m.actions[id]
		if !ok || !now.Before(entry.RetainedUntil) {
			delete(m.configRollbacks, id)
			continue
		}
		if entry.Action != "operation_config_rollback_guarded" || entry.Apply.State == "not_attempted" || ref.Binding != actionOutcomeBinding(entry) {
			return invalid("invalid_plan", "rollback outcome binding changed")
		}
	}
	return nil
}
func configRollbackOutcomeMatches(entry actionRecord, ref configRollbackLookup, receipt api.ConfigRollbackReceipt) bool {
	want := ref.ExpectedRevision
	if receipt.Changed {
		want++
	}
	return receipt.RequestID == ref.RequestID && receipt.AppID == entry.AppID && receipt.Scope == entry.Scope && receipt.EnvironmentID == entry.EnvironmentID && receipt.RestoredRevision == ref.TargetRevision && receipt.ConfigRevision == want
}
func (m *management) readConfigRollbackOutcome(ctx context.Context, entry actionRecord, ref configRollbackLookup, out *actionOutcome) {
	receipt, err := m.client.GetConfigRollbackReceipt(ctx, entry.App, entry.Scope, ref.RequestID, ref.ReceiptProof)
	if err != nil {
		out.Reason = "receipt_unavailable"
		return
	}
	if !configRollbackOutcomeMatches(entry, ref, receipt) {
		out.Reason = "receipt_unverified"
		return
	}
	out.Outcome, out.Receipt = "verified_receipt", &actionReceipt{ConfigRevision: &receipt.ConfigRevision}
	out.ConfigChanged = &receipt.Changed
	out.Instruction = "This confirms the original configuration rollback only. Current configuration, secret values, restart and health require separate observation. Missing evidence never proves non-commit."
}
