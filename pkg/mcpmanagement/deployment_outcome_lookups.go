package mcpmanagement

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const deploymentOutcomeNamespace = "deployment-outcome-lookups/"

type deploymentOutcomeLookup struct {
	AttemptID    string
	ArtifactHash string
	Binding      string
}
type deploymentOutcomeSnapshot struct {
	Version int
	Items   map[string]deploymentOutcomeLookup
}

func deploymentOutcomeAction(action string) bool {
	return action == "deploy" || action == "workload_rollout"
}

func deploymentOutcomeBinding(entry actionRecord) string {
	body, _ := json.Marshal(struct {
		Target, Action string
		Workload       *actionWorkload
	}{budgetOutcomeBinding(entry), entry.Action, entry.Workload})
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}
func actionOutcomeBinding(entry actionRecord) string {
	if deploymentOutcomeAction(entry.Action) {
		return deploymentOutcomeBinding(entry)
	}
	return budgetOutcomeBinding(entry)
}
func deploymentArtifactHash(kind, identity string) string {
	body, _ := json.Marshal([]string{kind, identity})
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}
func planArtifactHash(p *releasePlan) string {
	if p.Source != nil {
		return deploymentArtifactHash("github", p.Source.CommitSHA)
	}
	return deploymentArtifactHash("image", p.Image)
}
func validDeploymentOutcomeLookup(ref deploymentOutcomeLookup) bool {
	return api.ValidDeploymentAttemptID(ref.AttemptID) && validOutcomeHash(ref.ArtifactHash) && validOutcomeHash(ref.Binding)
}

// Called under m.mu, before recording the attempt and before any native write.
// Never invent a native attempt identity for a previously attempted legacy plan.
func (m *management) prepareDeploymentSubmission(ctx context.Context, p *releasePlan) (context.Context, error) {
	if m.deploymentOutcomes == nil {
		m.deploymentOutcomes = map[string]deploymentOutcomeLookup{}
	}
	ref, found := m.deploymentOutcomes[p.ID]
	if !found {
		if p.attempted {
			return ctx, nil
		}
		var bytes [16]byte
		if _, err := rand.Read(bytes[:]); err != nil {
			return nil, err
		}
		ref = deploymentOutcomeLookup{AttemptID: hex.EncodeToString(bytes[:]), ArtifactHash: planArtifactHash(p), Binding: deploymentOutcomeBinding(recordAction(p))}
		m.deploymentOutcomes[p.ID] = ref
	}
	if !validDeploymentOutcomeLookup(ref) || ref.Binding != deploymentOutcomeBinding(recordAction(p)) || ref.ArtifactHash != planArtifactHash(p) {
		return nil, invalid("invalid_plan", "original deployment attempt binding changed")
	}
	return api.ContextWithDeploymentAttempt(ctx, ref.AttemptID), nil
}

func (m *management) restoreDeploymentOutcomeLookups() error {
	var snapshot deploymentOutcomeSnapshot
	if err := m.opts.planStore.read(deploymentOutcomeNamespace+m.opts.planNamespace, &snapshot); err != nil {
		return err
	}
	if snapshot.Version != 1 && (snapshot.Version != 0 || len(snapshot.Items) != 0) || len(snapshot.Items) > api.ManagementMCPActionHistoryMax {
		return errors.New("invalid deployment outcome snapshot")
	}
	m.deploymentOutcomes = map[string]deploymentOutcomeLookup{}
	for id, ref := range snapshot.Items {
		if !validActionID(id) || !validDeploymentOutcomeLookup(ref) {
			return errors.New("invalid deployment outcome reference")
		}
		if entry, ok := m.actions[id]; ok && time.Now().Before(entry.RetainedUntil) && (!deploymentOutcomeAction(entry.Action) || ref.Binding != deploymentOutcomeBinding(entry)) {
			return errors.New("deployment outcome binding mismatch")
		}
		m.deploymentOutcomes[id] = ref
	}
	return nil
}
func (m *management) syncDeploymentOutcomeLookups(now time.Time) error {
	for id, ref := range m.deploymentOutcomes {
		entry, ok := m.actions[id]
		if !ok || !now.Before(entry.RetainedUntil) {
			delete(m.deploymentOutcomes, id)
			continue
		}
		if !deploymentOutcomeAction(entry.Action) || entry.Apply.State == "not_attempted" || !validDeploymentOutcomeLookup(ref) {
			return invalid("invalid_plan", "invalid deployment outcome history")
		}
		if p := m.plans[id]; p != nil && p.attempted && deploymentOutcomeAction(p.Action) {
			if ref.ArtifactHash != planArtifactHash(p) {
				return invalid("invalid_plan", "original deployment artifact changed")
			}
			ref.Binding = deploymentOutcomeBinding(entry)
			m.deploymentOutcomes[id] = ref
		}
		if ref.Binding != deploymentOutcomeBinding(entry) {
			return invalid("invalid_plan", "deployment outcome history binding changed")
		}
	}
	return nil
}

func (m *management) readDeploymentOutcome(ctx context.Context, entry actionRecord, ref deploymentOutcomeLookup, out *actionOutcome) {
	receipt, err := m.client.GetDeploymentSubmission(ctx, entry.App, ref.AttemptID)
	if err != nil {
		out.Reason = "receipt_unavailable"
		return
	}
	if receipt.AttemptID != ref.AttemptID || receipt.AppID != entry.AppID || validateSegment(receipt.DeploymentID, "deployment_id") != nil || receipt.AcceptedAt.IsZero() {
		out.Reason = "receipt_unverified"
		return
	}
	d, err := m.client.GetDeployment(ctx, receipt.DeploymentID)
	if err != nil {
		out.Reason = "deployment_unavailable"
		return
	}
	if !deploymentOutcomeMatches(entry, ref, d) || d.ID != receipt.DeploymentID {
		out.Reason = "receipt_unverified"
		return
	}
	out.Outcome = "verified_receipt"
	out.Receipt = &actionReceipt{DeploymentID: d.ID}
	if entry.Action == "workload_rollout" {
		environment := entry.Scope
		if environment == api.DefaultEnvScope {
			environment = ""
		}
		out.Observation = &savedPlanReference{Tool: "gregale_get_rollout", Arguments: actionObservationArgs{App: entry.App, Project: entry.Project, Environment: environment, DeploymentID: d.ID}}
	} else {
		out.Observation = &savedPlanReference{Tool: "gregale_get_deployment", Arguments: deploymentArgs{DeploymentID: d.ID}}
	}
}
func deploymentOutcomeMatches(entry actionRecord, ref deploymentOutcomeLookup, d api.DeploymentResponse) bool {
	if d.AppID != entry.AppID || normalizedScope(d.Scope) != entry.Scope {
		return false
	}
	if entry.Scope != api.DefaultEnvScope && (d.StagedReview == nil || d.StagedReview.AppID != entry.AppID || d.StagedReview.ProjectID != entry.ProjectID || d.StagedReview.EnvironmentID != entry.EnvironmentID || d.StagedReview.LatestDeploymentID != d.ID) {
		return false
	}
	if entry.Workload != nil && d.ID == entry.Workload.RetainedDeploymentID {
		return false
	}
	switch d.Kind {
	case "image":
		return immutableImage.MatchString(d.ImageDigest) && deploymentArtifactHash(d.Kind, d.ImageDigest) == ref.ArtifactHash
	case "github":
		return sourceCommit.MatchString(d.CommitSHA) && validateSegment(d.BuildID, "build_id") == nil && deploymentArtifactHash(d.Kind, d.CommitSHA) == ref.ArtifactHash
	}
	return false
}
