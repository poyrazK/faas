// adr: 20261105131434
package mcpmanagement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Separate namespaces allow older bridges to preserve these records without
// interpreting the new action kind or retaining a reusable mutation request.
const scalingActionsNamespace = "scaling-actions/"
const scalingOutcomeNamespace = "scaling-outcome-lookups/"

type scalingOutcomeLookup struct {
	RequestID        string
	ExpectedRevision int64
	SettingsHash     string
	Binding          string
}

func (m *management) readScalingOutcome(ctx context.Context, entry actionRecord, ref scalingOutcomeLookup, out *actionOutcome) {
	r, err := m.client.GetAppScalingReceipt(ctx, entry.App, entry.Scope, ref.RequestID)
	if err != nil {
		out.Reason = "receipt_unavailable"
		return
	}
	if !scalingOutcomeMatches(entry, ref, r) {
		out.Reason = "receipt_unverified"
		return
	}
	out.Outcome = "verified_receipt"
	out.Receipt = &actionReceipt{ScalingRevision: &r.Revision}
	out.Instruction = "The receipt confirms the original scaling configuration commit only. Inspect current configuration and runtime health separately; missing evidence never proves non-commit. Do not replace an ambiguous request or renew its write authority."
	environment := entry.Scope
	if environment == api.DefaultEnvScope {
		environment = ""
	}
	out.Observation = &savedPlanReference{Tool: "gregale_get_app", Arguments: appArgs{App: entry.App, Project: entry.Project, Environment: environment}}
}

func scalingOutcomeMatches(entry actionRecord, ref scalingOutcomeLookup, r api.AppScalingReceipt) bool {
	return r.RequestID == ref.RequestID && r.AppID == entry.AppID && r.Scope == entry.Scope && r.EnvironmentID == entry.EnvironmentID && r.PreviousRevision == ref.ExpectedRevision && r.Revision >= r.PreviousRevision && (entry.Scope != api.DefaultEnvScope || r.Revision > r.PreviousRevision) && !r.CommittedAt.IsZero() && scalingSettingsHash(r.Settings) == ref.SettingsHash
}

type scalingOutcomeSnapshot struct {
	Version int
	Items   map[string]scalingOutcomeLookup
}

func scalingSettingsHash(settings api.AppScalingSettings) string {
	data, _ := json.Marshal(settings)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func scalingExpectedSettings(p *releasePlan) (api.AppScalingSettings, error) {
	if p.Operation == nil {
		return api.AppScalingSettings{}, invalid("invalid_plan", "missing scaling operation")
	}
	req, err := scalingRequest(p)
	if err != nil {
		return api.AppScalingSettings{}, err
	}
	s := p.Operation.ScalingBefore.Settings
	if req.RAMMB != nil {
		s.RAMMB = *req.RAMMB
	}
	if req.CPUMillicores != nil {
		s.CPUMillicores = *req.CPUMillicores
	}
	if req.MaxConcurrency != nil {
		s.MaxConcurrency = *req.MaxConcurrency
	}
	if req.MinInstances != nil {
		s.MinInstances = *req.MinInstances
	}
	return s, nil
}

func validScalingOutcomeLookup(ref scalingOutcomeLookup) bool {
	id, err := uuid.Parse(ref.RequestID)
	return err == nil && id != uuid.Nil && id.String() == ref.RequestID && ref.ExpectedRevision >= 0 && validOutcomeHash(ref.SettingsHash) && validOutcomeHash(ref.Binding)
}

func (m *management) restoreScalingOutcomeLookups() error {
	var saved scalingOutcomeSnapshot
	if err := m.opts.planStore.read(scalingOutcomeNamespace+m.opts.planNamespace, &saved); err != nil {
		return err
	}
	if saved.Version != 1 && (saved.Version != 0 || len(saved.Items) != 0) || len(saved.Items) > api.ManagementMCPActionHistoryMax {
		return errors.New("invalid scaling outcome lookup snapshot")
	}
	m.scalingOutcomes = make(map[string]scalingOutcomeLookup)
	for id, ref := range saved.Items {
		if !validActionID(id) || !validScalingOutcomeLookup(ref) {
			return errors.New("invalid scaling outcome lookup")
		}
		if entry, ok := m.actions[id]; ok && time.Now().Before(entry.RetainedUntil) && (entry.Action != "operation_scale" || entry.Apply.State == "not_attempted" || ref.Binding != actionOutcomeBinding(entry)) {
			return errors.New("scaling outcome lookup binding mismatch")
		}
		m.scalingOutcomes[id] = ref
	}
	return nil
}

func (m *management) syncScalingOutcomeLookups(now time.Time) error {
	if m.scalingOutcomes == nil {
		m.scalingOutcomes = make(map[string]scalingOutcomeLookup)
	}
	for id, p := range m.plans {
		entry, ok := m.actions[id]
		if !ok || p.Action != "operation_scale" || !p.attempted || !now.Before(entry.RetainedUntil) {
			continue
		}
		// Older saved operations may predate native scaling checkpoints. They
		// cannot prove the original request's settings; preserve their history
		// without inventing a lookup or preventing the bridge from starting.
		if p.Operation == nil || p.Operation.ScalingBefore == nil {
			continue
		}
		settings, err := scalingExpectedSettings(p)
		if err != nil {
			return err
		}
		ref := scalingOutcomeLookup{RequestID: scalingRequestID(p), ExpectedRevision: p.Operation.ExpectedRevision, SettingsHash: scalingSettingsHash(settings), Binding: actionOutcomeBinding(entry)}
		if !validScalingOutcomeLookup(ref) {
			return invalid("invalid_plan", "invalid scaling outcome reference")
		}
		if old, exists := m.scalingOutcomes[id]; exists && old != ref {
			return invalid("invalid_plan", "scaling outcome reference changed")
		}
		m.scalingOutcomes[id] = ref
	}
	for id, ref := range m.scalingOutcomes {
		entry, ok := m.actions[id]
		if !ok || !now.Before(entry.RetainedUntil) {
			delete(m.scalingOutcomes, id)
			continue
		}
		if entry.Action != "operation_scale" || entry.Apply.State == "not_attempted" || ref.Binding != actionOutcomeBinding(entry) {
			return invalid("invalid_plan", "scaling outcome binding changed")
		}
	}
	return nil
}

func scalingActionPhase(p *releasePlan) actionPhase {
	phase := actionPhase{State: "not_attempted"}
	if !p.attempted {
		return phase
	}
	phase.State = "outcome_unknown"
	if p.localReceipt == nil {
		return phase
	}
	data, err := json.Marshal(p.localReceipt)
	if err != nil {
		return phase
	}
	var saved struct {
		Result api.AppScalingReceipt `json:"result"`
	}
	if json.Unmarshal(data, &saved) != nil || !validScalingReceipt(p, saved.Result) {
		return phase
	}
	return actionPhase{State: "committed", Receipt: &actionReceipt{ScalingRevision: &saved.Result.Revision}}
}

func validateScalingAction(entry actionRecord) error {
	r := entry.Apply.Receipt
	if entry.Action != "operation_scale" {
		if r != nil && r.ScalingRevision != nil || entry.Promotion != nil && entry.Promotion.Receipt != nil && entry.Promotion.Receipt.ScalingRevision != nil {
			return errors.New("unexpected scaling receipt")
		}
		return nil
	}
	if entry.Promotion != nil || entry.Workload != nil || entry.Budget != nil {
		return errors.New("invalid scaling action")
	}
	if r != nil && (entry.Apply.State != "committed" || r.ScalingRevision == nil || *r.ScalingRevision < 0 || !reflect.DeepEqual(r, &actionReceipt{ScalingRevision: r.ScalingRevision})) {
		return errors.New("invalid scaling receipt")
	}
	return nil
}
