package mcpmanagement

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const rollbackOutcomeNamespace = "rollback-outcome-lookups/"

type rollbackOutcomeLookup struct {
	AttemptID, RequestHash, Binding string
	TargetID, CurrentID             string
}
type rollbackOutcomeSnapshot struct {
	Version int
	Items   map[string]rollbackOutcomeLookup
}

func rollbackRequest(p *releasePlan) api.RollbackRequest {
	return api.RollbackRequest{TargetDeploymentID: &p.TargetDeploymentID, ExpectedCurrentDeploymentID: &p.ExpectedCurrentID, Reason: p.Reason}
}
func validRollbackOutcomeLookup(ref rollbackOutcomeLookup) bool {
	return api.ValidDeploymentAttemptID(ref.AttemptID) && validOutcomeHash(ref.RequestHash) && validOutcomeHash(ref.Binding) && validateSegment(ref.TargetID, "deployment_id") == nil && validateSegment(ref.CurrentID, "deployment_id") == nil && ref.TargetID != ref.CurrentID
}
func rollbackReferenceMatches(entry actionRecord, ref rollbackOutcomeLookup) bool {
	return entry.Action == "rollback" && entry.Scope == api.DefaultEnvScope && entry.Apply.State != "not_attempted" && validRollbackOutcomeLookup(ref) && ref.Binding == deploymentOutcomeBinding(entry)
}

// Called under m.mu, before persisting the original attempt and sending a POST.
func (m *management) prepareRollbackOutcome(ctx context.Context, p *releasePlan) (context.Context, error) {
	if p.RolloutMode != api.ExecutionModeRequest || p.Scope != api.DefaultEnvScope {
		return ctx, nil
	}
	if m.rollbackOutcomes == nil {
		m.rollbackOutcomes = map[string]rollbackOutcomeLookup{}
	}
	ref, found := m.rollbackOutcomes[p.ID]
	hash := api.RollbackRequestHash(p.AppID, rollbackRequest(p))
	if !found {
		if p.attempted {
			return ctx, nil
		}
		var raw [16]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return nil, err
		}
		ref = rollbackOutcomeLookup{AttemptID: hex.EncodeToString(raw[:]), RequestHash: hash, Binding: deploymentOutcomeBinding(recordAction(p)), TargetID: p.TargetDeploymentID, CurrentID: p.ExpectedCurrentID}
		m.rollbackOutcomes[p.ID] = ref
	}
	if !validRollbackOutcomeLookup(ref) || ref.Binding != deploymentOutcomeBinding(recordAction(p)) || ref.RequestHash != hash || ref.TargetID != p.TargetDeploymentID || ref.CurrentID != p.ExpectedCurrentID {
		return nil, invalid("invalid_plan", "original rollback attempt binding changed")
	}
	return api.ContextWithRollbackAttempt(ctx, ref.AttemptID), nil
}
func (m *management) restoreRollbackOutcomeLookups() error {
	var saved rollbackOutcomeSnapshot
	if err := m.opts.planStore.read(rollbackOutcomeNamespace+m.opts.planNamespace, &saved); err != nil {
		return err
	}
	if saved.Version != 1 && (saved.Version != 0 || len(saved.Items) != 0) || len(saved.Items) > api.ManagementMCPActionHistoryMax {
		return errors.New("invalid rollback outcome snapshot")
	}
	m.rollbackOutcomes = map[string]rollbackOutcomeLookup{}
	for id, ref := range saved.Items {
		if !validActionID(id) || !validRollbackOutcomeLookup(ref) {
			return errors.New("invalid rollback outcome reference")
		}
		if entry, ok := m.actions[id]; ok && time.Now().Before(entry.RetainedUntil) && !rollbackReferenceMatches(entry, ref) {
			p := m.plans[id]
			if p == nil || !rollbackReferenceMatches(recordAction(p), ref) {
				return errors.New("rollback outcome binding mismatch")
			}
		}
		m.rollbackOutcomes[id] = ref
	}
	return nil
}
func (m *management) syncRollbackOutcomeLookups(now time.Time) error {
	for id, ref := range m.rollbackOutcomes {
		entry, ok := m.actions[id]
		if !ok || !now.Before(entry.RetainedUntil) {
			delete(m.rollbackOutcomes, id)
			continue
		}
		if p := m.plans[id]; p != nil && p.Action == "rollback" && p.attempted {
			if p.RolloutMode != api.ExecutionModeRequest || p.Scope != api.DefaultEnvScope || ref.RequestHash != api.RollbackRequestHash(p.AppID, rollbackRequest(p)) || ref.TargetID != p.TargetDeploymentID || ref.CurrentID != p.ExpectedCurrentID {
				return invalid("invalid_plan", "original rollback request changed")
			}
			ref.Binding = deploymentOutcomeBinding(entry)
			m.rollbackOutcomes[id] = ref
		}
		if !rollbackReferenceMatches(entry, ref) {
			return invalid("invalid_plan", "invalid rollback outcome history")
		}
	}
	return nil
}
func (m *management) readRollbackOutcome(ctx context.Context, entry actionRecord, ref rollbackOutcomeLookup, out *actionOutcome) {
	operationID := uuid.MustParse(ref.AttemptID).String()
	r, err := m.client.GetRollbackOperation(ctx, entry.App, operationID)
	if err != nil {
		out.Reason = "receipt_unavailable"
		return
	}
	request := api.RollbackRequest{TargetDeploymentID: &r.TargetDeploymentID, ExpectedCurrentDeploymentID: &r.CurrentDeploymentID, Reason: r.Reason}
	if r.ID != operationID || r.AppID != entry.AppID || r.Scope != api.DefaultEnvScope || r.Service || r.Trigger != "" || r.CreatedAt.IsZero() || !promotionServingMatches(r.TargetDeploymentID, ref.TargetID) || !promotionServingMatches(r.CurrentDeploymentID, ref.CurrentID) || api.RollbackRequestHash(r.AppID, request) != ref.RequestHash {
		out.Reason = "receipt_unverified"
		return
	}
	out.Outcome = "verified_receipt"
	out.Receipt = &actionReceipt{DeploymentID: ref.TargetID, RollbackOperationID: operationID}
	out.Observation = &savedPlanReference{Tool: "gregale_get_rollback", Arguments: rollbackArgs{App: entry.App, OperationID: operationID}}
}
