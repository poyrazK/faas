package state

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ EnvironmentGitOpsQualificationDiscoveryStore = (*MemStore)(nil)
var _ EnvironmentGitOpsQualificationDispatchStore = (*MemStore)(nil)
var _ EnvironmentGitOpsQualificationGraphDispatchStore = (*MemStore)(nil)

func (m *MemStore) ListEnvironmentWorkloadQualificationsForDispatch(ctx context.Context, nodeID, afterRequestID string, limit int) ([]string, error) {
	if !qualificationDispatchPageValid(nodeID, afterRequestID, limit) {
		return nil, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := []string{}
	now := time.Now()
	for _, memory := range m.environmentGitOps {
		source := memory.source
		if source.Spec.Mode != "enforce" || source.Suspended || !m.accounts[source.AccountID].MayDeploy() {
			continue
		}
		for id, request := range memory.qualifications {
			graph := memory.graphs[preparationGraphKey(request.FrozenInputs.Generation, request.FrozenInputs.PlanHash)]
			app := m.apps[request.AppID]
			if qualificationRecoveryCursor(id) <= qualificationRecoveryCursor(afterRequestID) || request.ExecutionMode == "job" || request.ExecutionMode == "worker" || len(request.FrozenInputs.ServiceBindings) != 0 ||
				qualificationSmokeAttemptRecorded(m, request) ||
				graph.ID != request.GraphID || graph.Phase != "prepared" || graph.EnvironmentID != source.EnvironmentID ||
				graph.Generation != source.Generation || graph.IntentVersion != source.IntentVersion || graph.RevisionID != source.ApprovedRevisionID ||
				app.ID == "" || app.AccountID != source.AccountID || app.ProjectID != source.ProjectID ||
				(app.Status != AppActive && app.Status != AppEvictedCold) ||
				(app.NodeID != "" && qualificationRecoveryCursor(app.NodeID) != qualificationRecoveryCursor(nodeID)) {
				continue
			}
			if request.Phase != "queued" && (request.Phase != "claimed" || request.LeaseUntil == nil || now.Before(*request.LeaseUntil)) {
				continue
			}
			if ins, exists := m.instances[request.ReservedInstanceID]; exists && !qualificationInstanceRetired(ins) {
				continue
			}
			if m.qualificationRequestUnretiredLocked(request.ID) {
				continue
			}
			ids = append(ids, id)
		}
	}
	slices.SortFunc(ids, func(a, b string) int {
		return strings.Compare(qualificationRecoveryCursor(a), qualificationRecoveryCursor(b))
	})
	return ids[:min(limit, len(ids))], ctx.Err()
}

func (m *MemStore) qualificationGraphDispatchMembersLocked(memory *environmentGitOpsMemory, graphID, nodeID string, now time.Time) ([]EnvironmentWorkloadQualificationRequest, bool) {
	source := memory.source
	if source.Spec.Mode != "enforce" || source.Suspended || !m.accounts[source.AccountID].MayDeploy() {
		return nil, false
	}
	var graph EnvironmentWorkloadGraph
	for _, candidate := range memory.graphs {
		if candidate.ID == graphID {
			graph = candidate
			break
		}
	}
	if graph.ID == "" || graph.Phase != "prepared" || graph.EnvironmentID != source.EnvironmentID || graph.Generation != source.Generation ||
		graph.IntentVersion != source.IntentVersion || graph.RevisionID != source.ApprovedRevisionID {
		return nil, false
	}
	requests := make([]EnvironmentWorkloadQualificationRequest, 0)
	for _, request := range memory.qualifications {
		if request.GraphID == graphID {
			requests = append(requests, request)
		}
	}
	slices.SortFunc(requests, func(a, b EnvironmentWorkloadQualificationRequest) int { return strings.Compare(a.Resource, b.Resource) })
	expected, completeSmokeReceipts := 0, 0
	for _, member := range graph.Members {
		if member.CandidateDeploymentID != "" {
			expected++
		}
	}
	if expected == 0 || len(requests) != expected {
		return nil, false
	}
	for _, request := range requests {
		if request.ExecutionMode == api.ExecutionModeJob && !qualificationGraphJobQueueBindingsSupported(graph, request.Resource) {
			return nil, false
		}
		key := qualificationSmokeReceiptKey(request.ID, request.Attempt)
		if request.ExecutionMode == api.ExecutionModeJob {
			_, recorded := m.qualificationJobSmokeReceipts[key]
			if recorded {
				completeSmokeReceipts++
			}
		} else if _, recorded := m.qualificationSmokeReceipts[key]; recorded {
			completeSmokeReceipts++
		}
		if request.Phase != "queued" && (request.Phase != "claimed" || request.LeaseUntil == nil || now.Before(*request.LeaseUntil)) {
			return nil, false
		}
		app := m.apps[request.AppID]
		if app.ID == "" || app.AccountID != source.AccountID || app.ProjectID != source.ProjectID ||
			(app.Status != AppActive && app.Status != AppEvictedCold) || (app.AppProtocol != "" && app.AppProtocol != api.AppProtocolHTTP1) ||
			(app.NodeID != "" && qualificationRecoveryCursor(app.NodeID) != qualificationRecoveryCursor(nodeID)) {
			return nil, false
		}
		if !qualificationGraphSmokePolicyValid(request) {
			return nil, false
		}
		if ins, exists := m.instances[request.ReservedInstanceID]; exists && !qualificationInstanceRetired(ins) || m.qualificationRequestUnretiredLocked(request.ID) {
			return nil, false
		}
	}
	if completeSmokeReceipts == len(requests) || m.qualificationCurrentLocked(memory, requests[0]) != nil {
		return nil, false
	}
	return requests, true
}

func qualificationSmokeAttemptRecorded(m *MemStore, request EnvironmentWorkloadQualificationRequest) bool {
	if m == nil || request.Attempt < 1 {
		return false
	}
	key := qualificationSmokeReceiptKey(request.ID, request.Attempt)
	_, smokeExists := m.qualificationSmokeReceipts[key]
	_, jobSmokeExists := m.qualificationJobSmokeReceipts[key]
	return smokeExists || jobSmokeExists
}

func (m *MemStore) ListEnvironmentWorkloadQualificationGraphsForDispatch(ctx context.Context, nodeID, afterGraphID string, limit int) ([]string, error) {
	if !qualificationGraphDispatchPageValid(nodeID, afterGraphID, limit) {
		return nil, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	ids := make([]string, 0)
	for _, memory := range m.environmentGitOps {
		for _, graph := range memory.graphs {
			if qualificationRecoveryCursor(graph.ID) <= qualificationRecoveryCursor(afterGraphID) {
				continue
			}
			if _, ok := m.qualificationGraphDispatchMembersLocked(memory, graph.ID, nodeID, now); ok {
				ids = append(ids, graph.ID)
			}
		}
	}
	slices.SortFunc(ids, func(a, b string) int {
		return strings.Compare(qualificationRecoveryCursor(a), qualificationRecoveryCursor(b))
	})
	ids = slices.Compact(ids)
	return ids[:min(limit, len(ids))], ctx.Err()
}

func (m *MemStore) ClaimEnvironmentWorkloadQualificationGraphForNode(ctx context.Context, graphID, nodeID, workerID string, duration time.Duration) ([]EnvironmentWorkloadQualificationRequest, error) {
	if !qualificationClaimArgumentsValid(graphID, workerID, duration) || !qualificationRecoveryUUIDValid(nodeID) {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, memory := range m.environmentGitOps {
		requests, ok := m.qualificationGraphDispatchMembersLocked(memory, graphID, nodeID, time.Now().UTC())
		if !ok {
			continue
		}
		until := time.Now().UTC().Add(duration)
		claimed := make([]EnvironmentWorkloadQualificationRequest, 0, len(requests))
		for _, current := range requests {
			current.Phase, current.WorkerID, current.LeaseToken = "claimed", workerID, uuid.NewString()
			current.LeaseUntil, current.ReservedInstanceID = &until, uuid.NewString()
			current.Attempt++
			memory.qualifications[current.ID] = cloneQualificationRequest(current)
			claimed = append(claimed, cloneQualificationRequest(current))
		}
		return claimed, nil
	}
	return nil, ErrConflict
}
