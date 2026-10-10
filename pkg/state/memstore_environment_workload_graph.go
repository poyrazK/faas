package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/environmentsync"
)

var _ EnvironmentGitOpsGraphPreparationStore = (*MemStore)(nil)

func (m *MemStore) ReconcileEnvironmentGitOpsPreparation(_ context.Context, lease EnvironmentGitOpsLease, reviewed environmentsync.Plan) (EnvironmentWorkloadGraph, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inputs, _, err := m.environmentCandidateInputsLocked(lease, reviewed)
	if err != nil {
		return EnvironmentWorkloadGraph{}, err
	}
	memory := m.environmentGitOps[lease.Source.ID]
	key := preparationGraphKey(lease.Source.Generation, reviewed.Hash)
	graph, exists := memory.graphs[key]
	if !exists {
		return graph, ErrNotFound
	}
	candidates := []EnvironmentWorkloadCandidate{}
	for _, input := range inputs {
		dep, err := m.environmentCandidateLocked(candidateFrozenInputs(input))
		if err != nil {
			return graph, err
		}
		if dep.ID == "" {
			return graph, ErrConflict
		}
		candidates = append(candidates, EnvironmentWorkloadCandidate{Status: dep.Status, HasRootfs: dep.RootfsPath != "" || dep.RootfsKey != ""})
	}
	phase, code := preparationGraphPhase(candidates)
	if graph.Phase != "failed" && graph.Phase != phase {
		if graph.Phase == "prepared" && phase == "preparing" {
			return graph, ErrConflict
		}
		graph.Phase, graph.ErrorCode, graph.PreparedAt = phase, code, nil
		if phase == "prepared" {
			now := time.Now().UTC()
			graph.PreparedAt = &now
		}
		memory.graphs[key] = graph
	}
	return clonePreparationGraph(graph), nil
}
