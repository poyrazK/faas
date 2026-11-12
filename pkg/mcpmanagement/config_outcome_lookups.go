// adr: 20261105140140
package mcpmanagement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const configActionsNamespace = "config-actions/"
const configOutcomeNamespace = "config-outcome-lookups/"

// Configuration values and key names are never stored in action history.
type actionConfig struct {
	RestartRequested bool `json:"restart_requested"`
}
type configOutcomeLookup struct {
	RequestID        string
	ReceiptProof     string
	ExpectedRevision int64
	KeysHash         string
	Binding          string
}
type configOutcomeSnapshot struct {
	Version int
	Items   map[string]configOutcomeLookup
}

func configOutcomeBinding(entry actionRecord) string {
	data, _ := json.Marshal(struct {
		Target, Action string
		Config         *actionConfig
	}{budgetOutcomeBinding(entry), entry.Action, entry.Config})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func configKeysHash(keys []string) string {
	data, _ := json.Marshal(keys)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func validConfigOutcomeLookup(ref configOutcomeLookup) bool {
	id, err := uuid.Parse(ref.RequestID)
	return err == nil && id != uuid.Nil && id.String() == ref.RequestID && api.ValidEnvBatchReceiptProof(ref.ReceiptProof) && ref.ExpectedRevision >= 0 && validOutcomeHash(ref.KeysHash) && validOutcomeHash(ref.Binding)
}

func configActionPhase(p *releasePlan) (*actionConfig, actionPhase) {
	phase := actionPhase{State: "not_attempted"}
	if p.Operation == nil {
		return nil, phase
	}
	var original planEnvChangeArgs
	if json.Unmarshal(p.Operation.Request, &original) != nil {
		return nil, phase
	}
	view := &actionConfig{RestartRequested: original.Restart}
	if !p.attempted {
		return view, phase
	}
	phase.State = "outcome_unknown"
	if (p.Operation.EnvIdentityPinned || p.Scope == api.DefaultEnvScope) && p.configReceipt != nil && validConfigCheckpoint(p, p.configReceipt) {
		phase = actionPhase{State: "committed", Receipt: &actionReceipt{ConfigRevision: &p.configReceipt.ConfigChange.Revision}}
	}
	return view, phase
}

func validateConfigAction(entry actionRecord) error {
	if entry.Action == "operation_config_rollback_guarded" {
		return nil
	}
	r := entry.Apply.Receipt
	if entry.Action != "operation_env" {
		if entry.Config != nil || r != nil && r.ConfigRevision != nil || entry.Promotion != nil && entry.Promotion.Receipt != nil && entry.Promotion.Receipt.ConfigRevision != nil {
			return errors.New("unexpected configuration history")
		}
		return nil
	}
	if entry.Config == nil || entry.Promotion != nil || entry.Workload != nil || entry.Budget != nil {
		return errors.New("invalid configuration history")
	}
	if r != nil && (entry.Apply.State != "committed" || r.ConfigRevision == nil || *r.ConfigRevision < 1 || !reflect.DeepEqual(r, &actionReceipt{ConfigRevision: r.ConfigRevision})) {
		return errors.New("invalid configuration receipt")
	}
	return nil
}

func (m *management) restoreConfigOutcomeLookups() error {
	var saved configOutcomeSnapshot
	if err := m.opts.planStore.read(configOutcomeNamespace+m.opts.planNamespace, &saved); err != nil {
		return err
	}
	if saved.Version != 1 && (saved.Version != 0 || len(saved.Items) != 0) || len(saved.Items) > api.ManagementMCPActionHistoryMax {
		return errors.New("invalid configuration outcome snapshot")
	}
	m.configOutcomes = make(map[string]configOutcomeLookup)
	for id, ref := range saved.Items {
		if !validActionID(id) || !validConfigOutcomeLookup(ref) {
			return errors.New("invalid configuration outcome reference")
		}
		if entry, ok := m.actions[id]; ok && time.Now().Before(entry.RetainedUntil) && (entry.Action != "operation_env" || entry.Apply.State == "not_attempted" || ref.Binding != configOutcomeBinding(entry)) {
			return errors.New("configuration outcome binding mismatch")
		}
		m.configOutcomes[id] = ref
	}
	return nil
}

func (m *management) syncConfigOutcomeLookups(now time.Time) error {
	if m.configOutcomes == nil {
		m.configOutcomes = make(map[string]configOutcomeLookup)
	}
	for id, p := range m.plans {
		entry, ok := m.actions[id]
		if !ok || p.Action != "operation_env" || !p.attempted || !now.Before(entry.RetainedUntil) || p.Operation == nil || !p.Operation.EnvIdentityPinned {
			continue
		}
		var original planEnvChangeArgs
		if json.Unmarshal(p.Operation.Request, &original) != nil || len(original.Values) == 0 || !slices.Equal(slices.Sorted(maps.Keys(original.Values)), p.Operation.Keys) {
			return invalid("invalid_plan", "configuration outcome keys do not match the original plan")
		}
		ref := configOutcomeLookup{RequestID: envRequestID(p), ReceiptProof: p.Operation.EnvReceiptProof, ExpectedRevision: p.Operation.ExpectedRevision, KeysHash: configKeysHash(p.Operation.Keys), Binding: configOutcomeBinding(entry)}
		if !validConfigOutcomeLookup(ref) {
			return invalid("invalid_plan", "invalid configuration outcome reference")
		}
		if previous, exists := m.configOutcomes[id]; exists && previous != ref {
			return invalid("invalid_plan", "configuration outcome reference changed")
		}
		m.configOutcomes[id] = ref
	}
	for id, ref := range m.configOutcomes {
		entry, ok := m.actions[id]
		if !ok || !now.Before(entry.RetainedUntil) {
			delete(m.configOutcomes, id)
			continue
		}
		if entry.Action != "operation_env" || entry.Apply.State == "not_attempted" || ref.Binding != configOutcomeBinding(entry) {
			return invalid("invalid_plan", "configuration outcome binding changed")
		}
	}
	return nil
}

func configOutcomeMatches(entry actionRecord, ref configOutcomeLookup, r api.EnvBatchResponse) bool {
	wantRevision := ref.ExpectedRevision + 1
	if ref.ExpectedRevision == 0 {
		wantRevision = 2
	}
	return r.RequestID == ref.RequestID && r.AppID == entry.AppID && r.Scope == entry.Scope && r.EnvironmentID == entry.EnvironmentID && r.ConfigChange.Revision == wantRevision && len(r.DeletedKeys) == 0 && configKeysHash(r.UpdatedKeys) == ref.KeysHash
}

func (m *management) readConfigOutcome(ctx context.Context, entry actionRecord, ref configOutcomeLookup, out *actionOutcome) {
	r, err := m.client.GetEnvBatchReceipt(ctx, entry.App, entry.Scope, ref.RequestID, ref.ReceiptProof)
	if err != nil {
		out.Reason = "receipt_unavailable"
		return
	}
	if !configOutcomeMatches(entry, ref, r) {
		out.Reason = "receipt_unverified"
		return
	}
	out.Outcome = "verified_receipt"
	out.Receipt = &actionReceipt{ConfigRevision: &r.ConfigChange.Revision}
	out.Instruction = "This receipt confirms the original configuration commit only. Current configuration and any requested restart or runtime health remain separate and unobserved. Missing evidence never proves non-commit; do not replay or replace the original request."
}
