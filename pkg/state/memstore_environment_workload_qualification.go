package state

import (
	"context"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

var _ EnvironmentGitOpsQualificationStore = (*MemStore)(nil)

func (m *MemStore) QueueEnvironmentGitOpsQualification(_ context.Context, lease EnvironmentGitOpsLease, reviewed environmentsync.Plan) ([]EnvironmentWorkloadQualificationRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, _, err := m.environmentCandidateInputsLocked(lease, reviewed); err != nil {
		return nil, err
	}
	memory := m.environmentGitOps[lease.Source.ID]
	graph, exists := memory.graphs[preparationGraphKey(lease.Source.Generation, reviewed.Hash)]
	if !exists || graph.Phase != "prepared" {
		return nil, ErrConflict
	}
	// Complete validation before any publication, matching the Pg transaction.
	requests := []EnvironmentWorkloadQualificationRequest{}
	for _, member := range graph.Members {
		if member.CandidateDeploymentID == "" {
			continue
		}
		request, err := qualificationRequest(graph, member, m.deployments[member.CandidateDeploymentID])
		if err != nil {
			return nil, err
		}
		for _, prior := range memory.qualifications {
			if prior.GraphID == graph.ID && prior.Resource == member.Resource {
				if prior.Artifact != request.Artifact || !reflect.DeepEqual(prior.FrozenInputs, request.FrozenInputs) {
					return nil, ErrConflict
				}
				request = prior
				break
			}
		}
		requests = append(requests, request)
	}
	if memory.qualifications == nil {
		memory.qualifications = map[string]EnvironmentWorkloadQualificationRequest{}
	}
	for i, request := range requests {
		if request.ID == "" {
			request.ID, request.CreatedAt = newID(), time.Now().UTC()
			memory.qualifications[request.ID] = cloneQualificationRequest(request)
		}
		requests[i] = cloneQualificationRequest(request)
	}
	return requests, nil
}

func (m *MemStore) qualificationLocked(id string) (*environmentGitOpsMemory, EnvironmentWorkloadQualificationRequest, error) {
	for _, memory := range m.environmentGitOps {
		if request, exists := memory.qualifications[id]; exists {
			return memory, request, nil
		}
	}
	return nil, EnvironmentWorkloadQualificationRequest{}, ErrNotFound
}

func (m *MemStore) qualificationCurrentLocked(memory *environmentGitOpsMemory, request EnvironmentWorkloadQualificationRequest) error {
	source := memory.source
	if !m.accounts[source.AccountID].MayDeploy() {
		return ErrConflict
	}
	graph, exists := memory.graphs[preparationGraphKey(request.FrozenInputs.Generation, request.FrozenInputs.PlanHash)]
	if !exists || graph.ID != request.GraphID || graph.Phase != "prepared" || source.Spec.Mode != "enforce" || source.Suspended ||
		graph.Generation != source.Generation || graph.IntentVersion != source.IntentVersion || graph.RevisionID != source.ApprovedRevisionID {
		return ErrConflict
	}
	desired, err := desiredEnvironmentRevision(memory.revisions[source.ApprovedRevisionID])
	if err != nil {
		return err
	}
	observed, err := compileGitOpsObservation(m.gitOpsSnapshotLocked(memory), desired)
	if err != nil {
		return err
	}
	plan, err := environmentGitOpsPlan(source, memory.revisions[source.ApprovedRevisionID], desired, observed, false)
	if err != nil || !plan.CanApply() || plan.HasDrift() || plan.Hash != graph.PlanHash {
		return ErrConflict
	}
	for _, member := range graph.Members {
		if member.CandidateDeploymentID == "" {
			continue
		}
		var captured EnvironmentWorkloadQualificationRequest
		for _, candidate := range memory.qualifications {
			if candidate.GraphID == graph.ID && candidate.Resource == member.Resource {
				captured = candidate
				break
			}
		}
		current, err := qualificationRequest(graph, member, m.deployments[member.CandidateDeploymentID])
		if err != nil || captured.ID == "" || current.Artifact != captured.Artifact || !reflect.DeepEqual(current.FrozenInputs, captured.FrozenInputs) {
			return ErrConflict
		}
	}
	return nil
}

func (m *MemStore) ClaimEnvironmentWorkloadQualification(_ context.Context, id, workerID string, duration time.Duration) (EnvironmentWorkloadQualificationRequest, error) {
	if !qualificationClaimArgumentsValid(id, workerID, duration) {
		return EnvironmentWorkloadQualificationRequest{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, current, err := m.qualificationLocked(id)
	if err != nil {
		return current, err
	}
	if err := m.qualificationCurrentLocked(memory, current); err != nil {
		return EnvironmentWorkloadQualificationRequest{}, err
	}
	now := time.Now().UTC()
	if current.Phase == "claimed" && current.LeaseUntil != nil && now.Before(*current.LeaseUntil) {
		return EnvironmentWorkloadQualificationRequest{}, ErrConflict
	}
	if ins, exists := m.instances[current.ReservedInstanceID]; exists && !qualificationInstanceRetired(ins) {
		return EnvironmentWorkloadQualificationRequest{}, ErrConflict
	}
	if m.qualificationExecutionUnretiredLocked(current.ReservedInstanceID) {
		return EnvironmentWorkloadQualificationRequest{}, ErrConflict
	}
	current.ReservedInstanceID = ""
	if current.ExecutionMode != "job" {
		current.ReservedInstanceID = uuid.NewString()
	}
	until := now.Add(duration)
	current.Phase, current.WorkerID, current.LeaseToken, current.LeaseUntil = "claimed", workerID, uuid.NewString(), &until
	current.Attempt++
	memory.qualifications[id] = cloneQualificationRequest(current)
	return cloneQualificationRequest(current), nil
}

func qualificationInstanceRetired(ins Instance) bool {
	return State(ins.State) == StateParked || State(ins.State) == StateStopped || State(ins.State) == StateFailed
}

func (m *MemStore) ValidateEnvironmentWorkloadQualification(_ context.Context, claimed EnvironmentWorkloadQualificationRequest) error {
	if _, err := uuid.Parse(claimed.ID); err != nil {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, current, err := m.qualificationLocked(claimed.ID)
	if err != nil {
		return err
	}
	if !qualificationLeaseMatches(current, claimed, time.Now()) {
		return ErrConflict
	}
	return m.qualificationCurrentLocked(memory, current)
}

func (m *MemStore) RenewEnvironmentWorkloadQualification(_ context.Context, claimed EnvironmentWorkloadQualificationRequest, duration time.Duration) (EnvironmentWorkloadQualificationRequest, error) {
	if !qualificationClaimArgumentsValid(claimed.ID, claimed.WorkerID, duration) {
		return EnvironmentWorkloadQualificationRequest{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, current, err := m.qualificationLocked(claimed.ID)
	if err != nil {
		return current, err
	}
	now := time.Now().UTC()
	if !qualificationLeaseMatches(current, claimed, now) {
		return EnvironmentWorkloadQualificationRequest{}, ErrConflict
	}
	if err := m.qualificationCurrentLocked(memory, current); err != nil {
		return EnvironmentWorkloadQualificationRequest{}, err
	}
	until := now.Add(duration)
	if until.After(*current.LeaseUntil) {
		current.LeaseUntil = &until
	}
	memory.qualifications[current.ID] = cloneQualificationRequest(current)
	return cloneQualificationRequest(current), nil
}
