package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/environmentsync"
)

var _ EnvironmentGitOpsActivationEvidenceStore = (*MemStore)(nil)

func (m *MemStore) EnvironmentGitOpsActivationEvidence(ctx context.Context, lease EnvironmentGitOpsLease, reviewed environmentsync.Plan) (EnvironmentWorkloadActivationEvidence, error) {
	if err := ctx.Err(); err != nil {
		return EnvironmentWorkloadActivationEvidence{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, _, err := m.environmentCandidateInputsLocked(lease, reviewed); err != nil {
		return EnvironmentWorkloadActivationEvidence{}, err
	}
	memory := m.environmentGitOps[lease.Source.ID]
	graph, exists := memory.graphs[preparationGraphKey(lease.Source.Generation, reviewed.Hash)]
	if !exists {
		return EnvironmentWorkloadActivationEvidence{}, ErrNotFound
	}
	captures := map[string]EnvironmentQualificationSnapshotReceipt{}
	for _, request := range memory.qualifications {
		if request.GraphID != graph.ID || m.qualificationCurrentLocked(memory, request) != nil {
			continue
		}
		receipt, exists := m.qualificationSnapshots[request.ReservedInstanceID]
		if exists && receipt.Execution.RequestID == request.ID && receipt.Execution.Attempt == request.Attempt && receipt.Execution.Artifact == request.Artifact &&
			m.runtimeConfigInputsFreshLocked(request.AppID, receipt.Inputs) {
			captures[request.DeploymentID] = receipt
		}
	}
	return graphActivationEvidence(graph, captures), nil
}
