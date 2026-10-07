package state

import "context"

var _ EnvironmentGitApprovalStore = (*MemStore)(nil)

func (memory *environmentGitOpsMemory) approvalForRevision(revisionID string) (EnvironmentGitRevisionApproval, bool) {
	var found EnvironmentGitRevisionApproval
	for _, record := range memory.approvals {
		if record.RevisionID == revisionID && record.Generation <= memory.source.Generation && record.Generation > found.Generation {
			found = record
		}
	}
	return cloneEnvironmentGitApproval(found), found.ID != ""
}

func (m *MemStore) EnvironmentGitRevisionApproval(_ context.Context, accountID, sourceID, revisionID string) (EnvironmentGitRevisionApproval, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	memory := m.environmentGitOps[sourceID]
	if memory == nil || memory.source.AccountID != accountID {
		return EnvironmentGitRevisionApproval{}, ErrNotFound
	}
	if record, ok := memory.approvalForRevision(revisionID); ok {
		return record, nil
	}
	return EnvironmentGitRevisionApproval{}, ErrNotFound
}
