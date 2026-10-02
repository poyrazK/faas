package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/environmentsync"
)

var _ EnvironmentGitOpsPreparationStore = (*MemStore)(nil)

func (m *MemStore) environmentCandidateInputsLocked(lease EnvironmentGitOpsLease, reviewed environmentsync.Plan) ([]Deployment, gitOpsIntentSnapshot, error) {
	memory, err := m.gitOpsLeaseLocked(lease, time.Now())
	if err != nil {
		return nil, gitOpsIntentSnapshot{}, err
	}
	revision := memory.revisions[memory.source.ApprovedRevisionID]
	desired, err := desiredEnvironmentRevision(revision)
	if err != nil {
		return nil, gitOpsIntentSnapshot{}, err
	}
	snapshot := m.gitOpsSnapshotLocked(memory)
	observed, err := compileGitOpsObservation(snapshot, desired)
	if err != nil {
		return nil, gitOpsIntentSnapshot{}, err
	}
	plan, err := environmentGitOpsPlan(memory.source, revision, desired, observed, false)
	if err != nil || plan.Hash != reviewed.Hash {
		return nil, gitOpsIntentSnapshot{}, ErrConflict
	}
	inputs, err := workloadCandidateInputs(memory.source, revision, desired, snapshot, plan)
	if err != nil {
		return nil, gitOpsIntentSnapshot{}, err
	}
	return inputs, snapshot, nil
}

func (m *MemStore) PrepareEnvironmentGitOpsImageCandidates(_ context.Context, lease EnvironmentGitOpsLease, reviewed environmentsync.Plan) ([]EnvironmentWorkloadCandidate, error) {
	return m.PrepareEnvironmentGitOpsCandidates(context.Background(), lease, reviewed, nil)
}

func (m *MemStore) PrepareEnvironmentGitOpsCandidates(_ context.Context, lease EnvironmentGitOpsLease, reviewed environmentsync.Plan, artifacts map[string]EnvironmentWorkloadSourceArtifact) ([]EnvironmentWorkloadCandidate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inputs, snapshot, err := m.environmentCandidateInputsLocked(lease, reviewed)
	if err != nil {
		return nil, err
	}
	// Validate every missing source before publishing any in-memory row.
	buildIDs := map[string]bool{}
	for i, input := range inputs {
		if m.environmentCandidateLocked(candidateFrozenInputs(input)).ID != "" {
			continue
		}
		inputs[i], err = attachCandidateSource(input, artifacts, snapshot.Plan)
		if err != nil {
			return nil, err
		}
		if inputs[i].BuildID != "" {
			if _, exists := m.builds[inputs[i].BuildID]; exists || buildIDs[inputs[i].BuildID] {
				return nil, ErrConflict
			}
			buildIDs[inputs[i].BuildID] = true
		}
	}
	// Allocate identities before publication so graph validation cannot leave
	// an in-memory candidate or build without its preparation cohort.
	prospective := make([]EnvironmentWorkloadCandidate, 0, len(inputs))
	for i, input := range inputs {
		frozen := candidateFrozenInputs(input)
		candidate := m.environmentCandidateLocked(frozen)
		if candidate.ID == "" {
			inputs[i].ID = newID()
			candidate = inputs[i]
		}
		prospective = append(prospective, EnvironmentWorkloadCandidate{DeploymentID: candidate.ID, AppID: candidate.AppID, Resource: frozen.Resource})
	}
	memory := m.environmentGitOps[lease.Source.ID]
	graph, err := preparationGraph(snapshot, memory.revisions[memory.source.ApprovedRevisionID], memory.source.Generation, reviewed.Hash, prospective)
	if err != nil {
		return nil, err
	}
	out := make([]EnvironmentWorkloadCandidate, 0, len(inputs))
	for _, input := range inputs {
		frozen := candidateFrozenInputs(input)
		candidate := m.environmentCandidateLocked(frozen)
		if candidate.ID == "" {
			// All fallible validation was completed before writing any row.
			input.Status, input.CreatedAt = DeployPending, time.Now().UTC()
			input.Revision = m.nextDeploymentRevisionLocked(input.AppID)
			input.TrafficPercentExplicit = true
			input.CanaryPreset, input.RolloutState = "none", "pending"
			input.StageState, _ = deploymentStageStateForCreate(nil, input.CreatedAt)
			if input.Kind != DeploymentKindImage {
				input.Status = DeployBuilding
				m.builds[input.BuildID] = Build{ID: input.BuildID, DeploymentID: input.ID, Kind: input.Kind, Status: BuildQueued,
					SourceBytes: input.SourceBytes, LogPath: artifacts[frozen.Resource].LogPath, EnqueuedAt: input.CreatedAt}
			}
			m.deployments[input.ID] = input
			candidate = input
		}
		out = append(out, EnvironmentWorkloadCandidate{DeploymentID: candidate.ID, BuildID: candidate.BuildID, AppID: candidate.AppID, Resource: frozen.Resource,
			Status: candidate.Status, HasRootfs: candidate.RootfsPath != "" || candidate.RootfsKey != ""})
	}
	if memory.graphs == nil {
		memory.graphs = map[string]EnvironmentWorkloadGraph{}
	}
	key := preparationGraphKey(graph.Generation, graph.PlanHash)
	if _, exists := memory.graphs[key]; !exists {
		graph.ID, graph.CreatedAt = newID(), time.Now().UTC()
		memory.graphs[key] = clonePreparationGraph(graph)
	}
	return out, nil
}

func (m *MemStore) environmentCandidateLocked(frozen EnvironmentWorkloadRuntime) Deployment {
	for _, dep := range m.deployments {
		prior, err := dep.ScopedWorkloadRuntime()
		if err == nil && prior != nil && prior.SourceID == frozen.SourceID && prior.Generation == frozen.Generation && prior.Resource == frozen.Resource && prior.PlanHash == frozen.PlanHash {
			return dep
		}
	}
	return Deployment{}
}

func (m *MemStore) EnvironmentGitOpsSourceRequests(_ context.Context, lease EnvironmentGitOpsLease, reviewed environmentsync.Plan) ([]EnvironmentWorkloadSourceRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inputs, _, err := m.environmentCandidateInputsLocked(lease, reviewed)
	if err != nil {
		return nil, err
	}
	var requests []EnvironmentWorkloadSourceRequest
	for _, input := range inputs {
		if input.Kind == DeploymentKindImage || m.environmentCandidateLocked(candidateFrozenInputs(input)).ID != "" {
			continue
		}
		request, err := sourceRequest(input)
		if err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}
	return requests, nil
}
