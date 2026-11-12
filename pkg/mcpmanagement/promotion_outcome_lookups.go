package mcpmanagement

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"github.com/google/uuid"
	"reflect"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const promotionOutcomeNamespace = "promotion-outcome-lookups/"

type promotionOutcomeLookup struct {
	AttemptID, RequestHash, ArtifactHash, Binding string
	CandidateID, ExpectedServingID                string
}
type promotionOutcomeSnapshot struct {
	Version int
	Items   map[string]promotionOutcomeLookup
}

func validPromotionOutcomeLookup(ref promotionOutcomeLookup) bool {
	return api.ValidDeploymentAttemptID(ref.AttemptID) && validOutcomeHash(ref.RequestHash) && validOutcomeHash(ref.ArtifactHash) && validOutcomeHash(ref.Binding) && validateSegment(ref.CandidateID, "deployment_id") == nil && (ref.ExpectedServingID == "" || validateSegment(ref.ExpectedServingID, "deployment_id") == nil)
}
func promotionRequest(p *releasePlan) api.UpdateDeploymentTrafficRequest {
	request := api.UpdateDeploymentTrafficRequest{TrafficPercent: 100, ExpectedServingDeploymentID: &p.ExpectedCurrentID}
	if p.EnvironmentID != "" {
		request.ExpectedReview = p.result.StagedReview
	}
	return request
}
func promotionReferenceMatches(entry actionRecord, ref promotionOutcomeLookup) bool {
	return entry.Action == "deploy" && entry.Promotion != nil && entry.Promotion.State != "not_attempted" && validPromotionOutcomeLookup(ref) && ref.Binding == deploymentOutcomeBinding(entry) && entry.Apply.Receipt != nil && entry.Apply.Receipt.DeploymentID == ref.CandidateID
}

// Called under m.mu, before persisting the attempt and sending any traffic write.
func (m *management) preparePromotionOutcome(ctx context.Context, p *releasePlan) (context.Context, error) {
	if m.promotionOutcomes == nil {
		m.promotionOutcomes = map[string]promotionOutcomeLookup{}
	}
	ref, found := m.promotionOutcomes[p.ID]
	hash, err := api.DeploymentPromotionRequestHash(p.result.ID, promotionRequest(p))
	if err != nil {
		return nil, err
	}
	if !found {
		if p.promotionAttempted {
			return ctx, nil
		}
		var raw [16]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return nil, err
		}
		ref = promotionOutcomeLookup{AttemptID: hex.EncodeToString(raw[:]), RequestHash: hash, ArtifactHash: planArtifactHash(p), Binding: deploymentOutcomeBinding(recordAction(p)), CandidateID: p.result.ID, ExpectedServingID: p.ExpectedCurrentID}
		m.promotionOutcomes[p.ID] = ref
	}
	if !validPromotionOutcomeLookup(ref) || ref.Binding != deploymentOutcomeBinding(recordAction(p)) || ref.RequestHash != hash || ref.CandidateID != p.result.ID || ref.ExpectedServingID != p.ExpectedCurrentID || ref.ArtifactHash != planArtifactHash(p) {
		return nil, invalid("invalid_plan", "original promotion attempt binding changed")
	}
	return api.ContextWithDeploymentPromotionAttempt(ctx, ref.AttemptID), nil
}
func (m *management) restorePromotionOutcomeLookups() error {
	var saved promotionOutcomeSnapshot
	if err := m.opts.planStore.read(promotionOutcomeNamespace+m.opts.planNamespace, &saved); err != nil {
		return err
	}
	if saved.Version != 1 && (saved.Version != 0 || len(saved.Items) != 0) || len(saved.Items) > api.ManagementMCPActionHistoryMax {
		return errors.New("invalid promotion outcome snapshot")
	}
	m.promotionOutcomes = map[string]promotionOutcomeLookup{}
	for id, ref := range saved.Items {
		if !validActionID(id) || !validPromotionOutcomeLookup(ref) {
			return errors.New("invalid promotion outcome reference")
		}
		if entry, ok := m.actions[id]; ok && time.Now().Before(entry.RetainedUntil) && !promotionReferenceMatches(entry, ref) {
			p := m.plans[id]
			if p == nil || !promotionReferenceMatches(recordAction(p), ref) {
				return errors.New("promotion outcome binding mismatch")
			}
		}
		m.promotionOutcomes[id] = ref
	}
	return nil
}
func (m *management) syncPromotionOutcomeLookups(now time.Time) error {
	for id, ref := range m.promotionOutcomes {
		entry, ok := m.actions[id]
		if !ok || !now.Before(entry.RetainedUntil) {
			delete(m.promotionOutcomes, id)
			continue
		}
		if p := m.plans[id]; p != nil && p.Action == "deploy" && p.promotionAttempted && p.result != nil {
			hash, err := api.DeploymentPromotionRequestHash(p.result.ID, promotionRequest(p))
			if err != nil || hash != ref.RequestHash || ref.ArtifactHash != planArtifactHash(p) || ref.CandidateID != p.result.ID || ref.ExpectedServingID != p.ExpectedCurrentID {
				return invalid("invalid_plan", "original promotion request changed")
			}
			ref.Binding = deploymentOutcomeBinding(entry)
			m.promotionOutcomes[id] = ref
		}
		if !promotionReferenceMatches(entry, ref) {
			return invalid("invalid_plan", "invalid promotion outcome history")
		}
	}
	return nil
}
func (m *management) readPromotionOutcome(ctx context.Context, entry actionRecord, ref promotionOutcomeLookup, out *actionOutcome) {
	receipt, err := m.client.GetDeploymentPromotionReceipt(ctx, entry.App, ref.AttemptID)
	if err != nil {
		out.Reason = "receipt_unavailable"
		return
	}
	if receipt.AttemptID != ref.AttemptID || receipt.RequestHash != ref.RequestHash || receipt.AppID != entry.AppID || receipt.DeploymentID != ref.CandidateID || !promotionServingMatches(receipt.ExpectedServingDeploymentID, ref.ExpectedServingID) || receipt.Scope != entry.Scope || receipt.ProjectID != entry.ProjectID || receipt.EnvironmentID != entry.EnvironmentID || receipt.AcceptedAt.IsZero() {
		out.Reason = "receipt_unverified"
		return
	}
	d, err := m.client.GetDeployment(ctx, ref.CandidateID)
	if err != nil {
		out.Reason = "deployment_unavailable"
		return
	}
	if d.ID != ref.CandidateID || !deploymentOutcomeMatches(entry, deploymentOutcomeLookup{ArtifactHash: ref.ArtifactHash}, d) {
		out.Reason = "receipt_unverified"
		return
	}
	out.Outcome = "verified_receipt"
	out.Receipt = &actionReceipt{DeploymentID: ref.CandidateID}
	out.Observation = &savedPlanReference{Tool: "gregale_get_deployment", Arguments: deploymentArgs{DeploymentID: ref.CandidateID}}
}

func (m *management) promotionRecoveryReference(entry actionRecord, now time.Time) *savedPlanReference {
	current, ok := m.actions[entry.PlanID]
	ref, found := m.promotionOutcomes[entry.PlanID]
	if !ok || !found || !now.Before(entry.RetainedUntil) || !now.Before(current.RetainedUntil) || !promotionReferenceMatches(entry, ref) || !promotionReferenceMatches(current, ref) || !reflect.DeepEqual(entry.Apply.Receipt, current.Apply.Receipt) {
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
	return &savedPlanReference{Tool: "gregale_get_action_outcome", Arguments: actionOutcomeArgs{getActionArgs: args, Phase: "promotion"}}
}

func promotionServingMatches(a, b string) bool {
	if a == b {
		return true
	}
	left, lerr := uuid.Parse(a)
	right, rerr := uuid.Parse(b)
	return lerr == nil && rerr == nil && left == right
}
