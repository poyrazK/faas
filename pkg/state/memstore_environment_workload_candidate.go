package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/environmentsync"
)

var _ EnvironmentGitOpsPreparationStore = (*MemStore)(nil)

func (m *MemStore) PrepareEnvironmentGitOpsImageCandidates(_ context.Context, lease EnvironmentGitOpsLease, reviewed environmentsync.Plan) ([]EnvironmentWorkloadCandidate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, err := m.gitOpsLeaseLocked(lease, time.Now())
	if err != nil {
		return nil, err
	}
	revision := memory.revisions[memory.source.ApprovedRevisionID]
	desired, err := desiredEnvironmentRevision(revision)
	if err != nil {
		return nil, err
	}
	snapshot := m.gitOpsSnapshotLocked(memory)
	observed, err := compileGitOpsObservation(snapshot, desired)
	if err != nil {
		return nil, err
	}
	plan, err := environmentGitOpsPlan(memory.source, revision, desired, observed, false)
	if err != nil || plan.Hash != reviewed.Hash {
		return nil, ErrConflict
	}
	inputs, err := imageCandidateInputs(memory.source, revision, desired, snapshot, plan)
	if err != nil {
		return nil, err
	}
	out := make([]EnvironmentWorkloadCandidate, 0, len(inputs))
	for _, input := range inputs {
		frozen, _ := input.ScopedWorkloadRuntime()
		var candidate Deployment
		for _, dep := range m.deployments {
			prior, err := dep.ScopedWorkloadRuntime()
			if err == nil && prior != nil && prior.SourceID == frozen.SourceID && prior.Generation == frozen.Generation && prior.Resource == frozen.Resource && prior.PlanHash == frozen.PlanHash {
				candidate = dep
				break
			}
		}
		if candidate.ID == "" {
			// All fallible validation was completed before writing any row.
			input.ID, input.Status, input.CreatedAt = newID(), DeployPending, time.Now().UTC()
			input.Revision = m.nextDeploymentRevisionLocked(input.AppID)
			input.TrafficPercentExplicit = true
			input.CanaryPreset, input.RolloutState = "none", "pending"
			input.StageState, _ = deploymentStageStateForCreate(nil, input.CreatedAt)
			m.deployments[input.ID] = input
			candidate = input
		}
		out = append(out, EnvironmentWorkloadCandidate{DeploymentID: candidate.ID, AppID: candidate.AppID, Resource: frozen.Resource,
			Status: candidate.Status, HasRootfs: candidate.RootfsPath != "" || candidate.RootfsKey != ""})
	}
	return out, nil
}
