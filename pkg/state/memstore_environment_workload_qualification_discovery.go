package state

import (
	"context"
	"slices"
	"strings"
	"time"
)

var _ EnvironmentGitOpsQualificationDiscoveryStore = (*MemStore)(nil)
var _ EnvironmentGitOpsQualificationDispatchStore = (*MemStore)(nil)

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
			if qualificationRecoveryCursor(id) <= qualificationRecoveryCursor(afterRequestID) || request.ExecutionMode == "job" || len(request.FrozenInputs.ServiceBindings) != 0 ||
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
			if m.qualificationExecutionUnretiredLocked(request.ReservedInstanceID) {
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
