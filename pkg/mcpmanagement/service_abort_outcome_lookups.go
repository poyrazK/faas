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

const serviceAbortOutcomeNamespace = "service-abort-outcome-lookups/"

type serviceAbortOutcomeLookup struct {
	AttemptID, RequestHash, Binding string
	TargetID, PredecessorID         string
}
type serviceAbortOutcomeSnapshot struct {
	Version int
	Items   map[string]serviceAbortOutcomeLookup
}

func serviceAbortRequest(p *releasePlan) api.RecoverRolloutRequest {
	return api.RecoverRolloutRequest{Action: "abort", DeploymentID: p.TargetDeploymentID, ExpectedPredecessorDeploymentID: p.PredecessorDeploymentID, Reason: p.Reason, ExpectedReview: p.review}
}

func validServiceAbortOutcomeLookup(ref serviceAbortOutcomeLookup) bool {
	return api.ValidDeploymentAttemptID(ref.AttemptID) && validOutcomeHash(ref.RequestHash) && validOutcomeHash(ref.Binding) && validateSegment(ref.TargetID, "deployment_id") == nil && validateSegment(ref.PredecessorID, "deployment_id") == nil && ref.TargetID != ref.PredecessorID
}
func serviceAbortReferenceMatches(entry actionRecord, ref serviceAbortOutcomeLookup) bool {
	return entry.Action == "service_abort" && entry.Scope == api.DefaultEnvScope && entry.Apply.State != "not_attempted" && entry.Workload != nil && entry.Workload.Mode == api.ExecutionModeService && entry.Workload.DeploymentID == ref.TargetID && entry.Workload.PredecessorDeploymentID == ref.PredecessorID && validServiceAbortOutcomeLookup(ref) && ref.Binding == deploymentOutcomeBinding(entry)
}

// Called under m.mu, before persisting the original attempt and sending a POST.
func (m *management) prepareServiceAbortOutcome(ctx context.Context, p *releasePlan) (context.Context, error) {
	if p.RolloutMode != api.ExecutionModeService || p.Scope != api.DefaultEnvScope {
		return ctx, nil
	}
	if m.serviceAbortOutcomes == nil {
		m.serviceAbortOutcomes = map[string]serviceAbortOutcomeLookup{}
	}
	ref, found := m.serviceAbortOutcomes[p.ID]
	hash := api.ServiceAbortRequestHash(p.AppID, serviceAbortRequest(p))
	if !found {
		if p.attempted {
			return ctx, nil
		}
		var raw [16]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return nil, err
		}
		ref = serviceAbortOutcomeLookup{AttemptID: hex.EncodeToString(raw[:]), RequestHash: hash, Binding: deploymentOutcomeBinding(recordAction(p)), TargetID: p.TargetDeploymentID, PredecessorID: p.PredecessorDeploymentID}
		m.serviceAbortOutcomes[p.ID] = ref
	}
	if !validServiceAbortOutcomeLookup(ref) || ref.Binding != deploymentOutcomeBinding(recordAction(p)) || ref.RequestHash != hash || ref.TargetID != p.TargetDeploymentID || ref.PredecessorID != p.PredecessorDeploymentID {
		return nil, invalid("invalid_plan", "original service abort attempt binding changed")
	}
	return api.ContextWithServiceAbortAttempt(ctx, ref.AttemptID), nil
}
func (m *management) restoreServiceAbortOutcomeLookups() error {
	var saved serviceAbortOutcomeSnapshot
	if err := m.opts.planStore.read(serviceAbortOutcomeNamespace+m.opts.planNamespace, &saved); err != nil {
		return err
	}
	if saved.Version != 1 && (saved.Version != 0 || len(saved.Items) != 0) || len(saved.Items) > api.ManagementMCPActionHistoryMax {
		return errors.New("invalid service abort outcome snapshot")
	}
	m.serviceAbortOutcomes = map[string]serviceAbortOutcomeLookup{}
	for id, ref := range saved.Items {
		if !validActionID(id) || !validServiceAbortOutcomeLookup(ref) {
			return errors.New("invalid service abort outcome reference")
		}
		if entry, ok := m.actions[id]; ok && time.Now().Before(entry.RetainedUntil) && !serviceAbortReferenceMatches(entry, ref) {
			p := m.plans[id]
			if p == nil || !serviceAbortReferenceMatches(recordAction(p), ref) {
				return errors.New("service abort outcome binding mismatch")
			}
		}
		m.serviceAbortOutcomes[id] = ref
	}
	return nil
}
func (m *management) syncServiceAbortOutcomeLookups(now time.Time) error {
	for id, ref := range m.serviceAbortOutcomes {
		entry, ok := m.actions[id]
		if !ok || !now.Before(entry.RetainedUntil) {
			delete(m.serviceAbortOutcomes, id)
			continue
		}
		if p := m.plans[id]; p != nil && p.Action == "service_abort" && p.attempted {
			if p.RolloutMode != api.ExecutionModeService || p.Scope != api.DefaultEnvScope || ref.RequestHash != api.ServiceAbortRequestHash(p.AppID, serviceAbortRequest(p)) || ref.TargetID != p.TargetDeploymentID || ref.PredecessorID != p.PredecessorDeploymentID {
				return invalid("invalid_plan", "original service abort request changed")
			}
			ref.Binding = deploymentOutcomeBinding(entry)
			m.serviceAbortOutcomes[id] = ref
		}
		if !serviceAbortReferenceMatches(entry, ref) {
			return invalid("invalid_plan", "invalid service abort outcome history")
		}
	}
	return nil
}
func (m *management) readServiceAbortOutcome(ctx context.Context, entry actionRecord, ref serviceAbortOutcomeLookup, out *actionOutcome) {
	r, err := m.client.GetServiceAbortReceipt(ctx, entry.App, ref.AttemptID)
	if err != nil {
		out.Reason = "receipt_unavailable"
		return
	}
	_, requestErr := uuid.Parse(r.RequestID)
	if requestErr != nil || r.AttemptID != ref.AttemptID || r.RequestHash != ref.RequestHash || !promotionServingMatches(r.AppID, entry.AppID) || r.Scope != api.DefaultEnvScope || r.AcceptedAt.IsZero() || r.RequestID == "" || validateSegment(r.RequestID, "request_id") != nil || !promotionServingMatches(r.DeploymentID, ref.TargetID) || !promotionServingMatches(r.PredecessorDeploymentID, ref.PredecessorID) {
		out.Reason = "receipt_unverified"
		return
	}
	if original := entry.Apply.Receipt; original != nil && original.RecoveryRequestID != "" && !promotionServingMatches(original.RecoveryRequestID, r.RequestID) {
		out.Reason = "receipt_unverified"
		return
	}
	out.Outcome = "verified_receipt"
	out.Receipt = &actionReceipt{DeploymentID: ref.TargetID, RecoveryRequestID: r.RequestID}
	out.Observation = &savedPlanReference{Tool: "gregale_get_rollout", Arguments: waitArgs{App: entry.App, DeploymentID: ref.TargetID}}
	out.Instruction += " Compare the observed reverse-handoff request ID with this receipt. A different current request cannot establish completion of the original accepted abort. Gateway ACK and request draining remain separate."
}
