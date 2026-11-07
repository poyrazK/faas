package mcpmanagement

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const environmentPromotionOutcomeNamespace = "environment-promotion-outcome-lookups/"

func environmentPromotionBinding(e environmentActionRecord) string {
	body, _ := json.Marshal([]string{e.PlanID, e.Action, e.AccountID, e.APIBase, e.Project, e.ProjectID, e.FromEnvironment, e.FromID, e.Environment, e.EnvironmentID, e.PlanExpiresAt.Format(time.RFC3339Nano)})
	return deploymentArtifactHash("environment_promotion", string(body))
}
func environmentPromotionReferenceMatches(e environmentActionRecord, ref environmentRollbackLookup) bool {
	return e.Action == "environment_promotion" && (e.Apply.State == "outcome_unknown" || e.Apply.State == "accepted") && validEnvironmentRollbackLookup(ref) && ref.Binding == environmentPromotionBinding(e) && ref.RequestHash == api.EnvironmentPromotionRequestHash(e.ProjectID, e.FromID, e.EnvironmentID, ref.PromotionHash)
}

// Called under m.mu before the original attempt/history commit and POST.
func (m *management) prepareEnvironmentPromotionOutcome(ctx context.Context, p *environmentPlan) (context.Context, error) {
	if m.environmentPromotionOutcomes == nil {
		m.environmentPromotionOutcomes = map[string]environmentRollbackLookup{}
	}
	ref, found := m.environmentPromotionOutcomes[p.ID]
	if !found {
		if p.attempted {
			return ctx, nil
		}
		var raw [16]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return nil, err
		}
		ref = environmentRollbackLookup{AttemptID: hex.EncodeToString(raw[:]), RequestHash: api.EnvironmentPromotionRequestHash(p.ProjectID, p.FromID, p.EnvironmentID, p.PromotionHash), PromotionHash: p.PromotionHash, Binding: environmentPromotionBinding(recordEnvironmentAction(p))}
		m.environmentPromotionOutcomes[p.ID] = ref
	}
	if !validEnvironmentRollbackLookup(ref) || ref.Binding != environmentPromotionBinding(recordEnvironmentAction(p)) || ref.PromotionHash != p.PromotionHash || ref.RequestHash != api.EnvironmentPromotionRequestHash(p.ProjectID, p.FromID, p.EnvironmentID, p.PromotionHash) {
		return nil, invalid("invalid_plan", "original environment promotion binding changed")
	}
	return api.ContextWithEnvironmentPromotionAttempt(ctx, ref.AttemptID, ref.RequestHash), nil
}
func (m *management) restoreEnvironmentPromotionOutcomes() error {
	var saved environmentRollbackSnapshot
	if err := m.opts.planStore.read(environmentPromotionOutcomeNamespace+m.opts.planNamespace, &saved); err != nil {
		return err
	}
	if saved.Version != 1 && (saved.Version != 0 || len(saved.Items) != 0) || len(saved.Items) > api.ManagementMCPActionHistoryMax {
		return errors.New("invalid environment promotion lookup snapshot")
	}
	m.environmentPromotionOutcomes = map[string]environmentRollbackLookup{}
	for id, ref := range saved.Items {
		if !validActionID(id) || !validEnvironmentRollbackLookup(ref) {
			return errors.New("invalid environment promotion lookup")
		}
		if entry, ok := m.environmentActions[id]; ok && time.Now().Before(entry.RetainedUntil) && !environmentPromotionReferenceMatches(entry, ref) {
			return errors.New("environment promotion lookup binding mismatch")
		}
		m.environmentPromotionOutcomes[id] = ref
	}
	return nil
}
func (m *management) syncEnvironmentPromotionOutcomes(now time.Time) error {
	for id, ref := range m.environmentPromotionOutcomes {
		entry, ok := m.environmentActions[id]
		if !ok || !now.Before(entry.RetainedUntil) {
			delete(m.environmentPromotionOutcomes, id)
			continue
		}
		if p := m.environmentPlans[id]; p != nil {
			if p.Action != "environment_promotion" || !p.attempted || ref.PromotionHash != p.PromotionHash || ref.RequestHash != api.EnvironmentPromotionRequestHash(p.ProjectID, p.FromID, p.EnvironmentID, p.PromotionHash) {
				return invalid("invalid_plan", "original environment promotion request changed")
			}
			ref.Binding = environmentPromotionBinding(entry)
			m.environmentPromotionOutcomes[id] = ref
		}
		if !environmentPromotionReferenceMatches(entry, ref) {
			return invalid("invalid_plan", "invalid original environment promotion lookup")
		}
	}
	return nil
}
func (m *management) environmentPromotionOutcomeReference(entry environmentActionRecord, now time.Time) *savedPlanReference {
	current, ok := m.environmentActions[entry.PlanID]
	ref, found := m.environmentPromotionOutcomes[entry.PlanID]
	if !ok || !found || !now.Before(entry.RetainedUntil) || !now.Before(current.RetainedUntil) || environmentHistoryBinding(current) != environmentHistoryBinding(entry) || !environmentPromotionReferenceMatches(entry, ref) {
		return nil
	}
	return &savedPlanReference{Tool: "gregale_get_environment_action_outcome", Arguments: getEnvironmentActionArgs{Project: entry.Project, Environment: entry.Environment, PlanID: entry.PlanID}}
}

func (m *management) readEnvironmentPromotionOutcome(ctx context.Context, e environmentActionRecord, ref environmentRollbackLookup, out *environmentActionOutcome) {
	receipt, err := m.client.GetEnvironmentPromotionReceipt(ctx, e.Project, e.Environment, ref.AttemptID)
	if err != nil {
		out.Reason = "receipt_unavailable"
		return
	}
	_, idErr := uuid.Parse(receipt.PromotionID)
	if idErr != nil || receipt.AttemptID != ref.AttemptID || receipt.RequestHash != ref.RequestHash || receipt.PromotionHash != ref.PromotionHash || receipt.ProjectID != e.ProjectID || receipt.FromEnvironmentID != e.FromID || receipt.EnvironmentID != e.EnvironmentID || receipt.PromotionID == "" || validateSegment(receipt.PromotionID, "promotion_id") != nil || e.PromotionID != "" && e.PromotionID != receipt.PromotionID || e.Apply.Receipt != nil && e.Apply.Receipt.PromotionID != receipt.PromotionID || receipt.AcceptedAt.IsZero() || api.EnvironmentPromotionRequestHash(receipt.ProjectID, receipt.FromEnvironmentID, receipt.EnvironmentID, receipt.PromotionHash) != ref.RequestHash {
		out.Reason = "receipt_unverified"
		return
	}
	out.Outcome = "verified_receipt"
	out.Receipt = &environmentActionReceipt{PromotionID: receipt.PromotionID}
	out.Observation = &environmentActionObservation{Tool: "gregale_get_environment_promotion", Arguments: environmentPromotionArgs{Project: e.Project, Environment: e.Environment, PromotionID: receipt.PromotionID}}
}
func (m *management) environmentActionOutcomeLookup(e environmentActionRecord) (environmentRollbackLookup, bool) {
	if e.Action == "environment_promotion" {
		ref, ok := m.environmentPromotionOutcomes[e.PlanID]
		return ref, ok
	}
	ref, ok := m.environmentRollbackOutcomes[e.PlanID]
	return ref, ok
}
func environmentActionReferenceMatches(e environmentActionRecord, ref environmentRollbackLookup) bool {
	if e.Action == "environment_promotion" {
		return environmentPromotionReferenceMatches(e, ref)
	}
	return environmentRollbackReferenceMatches(e, ref)
}
func environmentActionRequestBinding(e environmentActionRecord) string {
	if e.Action == "environment_promotion" {
		return environmentPromotionBinding(e)
	}
	return environmentRollbackBinding(e)
}
