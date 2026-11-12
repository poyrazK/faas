package state

import (
	"context"
	"sort"
	"time"
)

// ADR-20261031193635 slice 2: the branch data reconciler's MemStore state.

func (m *MemStore) ClaimProjectEnvironmentBranchData(_ context.Context, leaseToken string, now time.Time, lease time.Duration) (ProjectEnvironmentBranch, error) {
	if leaseToken == "" || lease <= 0 {
		return ProjectEnvironmentBranch{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var due []ProjectEnvironmentBranch
	for _, b := range m.projectEnvironmentBranches {
		if b.DataState == EnvironmentBranchDataPending && !b.dataNextAttemptAt.After(now) && !b.dataLeaseUntil.After(now) {
			due = append(due, b)
		}
	}
	if len(due) == 0 {
		return ProjectEnvironmentBranch{}, ErrNotFound
	}
	sort.Slice(due, func(i, j int) bool {
		if !due[i].dataNextAttemptAt.Equal(due[j].dataNextAttemptAt) {
			return due[i].dataNextAttemptAt.Before(due[j].dataNextAttemptAt)
		}
		return due[i].EnvironmentID < due[j].EnvironmentID
	})
	b := due[0]
	b.DataLeaseToken, b.dataLeaseUntil = leaseToken, now.Add(lease)
	m.projectEnvironmentBranches[b.EnvironmentID] = b
	return m.branchViewLocked(b), nil
}

func (m *MemStore) leasedBranchLocked(environmentID, leaseToken string) (ProjectEnvironmentBranch, error) {
	b, ok := m.projectEnvironmentBranches[environmentID]
	if !ok {
		return ProjectEnvironmentBranch{}, ErrNotFound
	}
	if b.DataLeaseToken != leaseToken || b.DataState != EnvironmentBranchDataPending {
		return ProjectEnvironmentBranch{}, ErrConflict
	}
	return b, nil
}

func (m *MemStore) CompleteProjectEnvironmentBranchData(_ context.Context, environmentID, leaseToken string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, err := m.leasedBranchLocked(environmentID, leaseToken)
	if err != nil {
		return err
	}
	b.DataState, b.DataError, b.DataLeaseToken, b.dataLeaseUntil = EnvironmentBranchDataReady, "", "", time.Time{}
	m.projectEnvironmentBranches[environmentID] = b
	return nil
}

func (m *MemStore) RetryProjectEnvironmentBranchData(_ context.Context, environmentID, leaseToken string, next time.Time, message string, failed bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, err := m.leasedBranchLocked(environmentID, leaseToken)
	if err != nil {
		return err
	}
	b.DataError, b.DataAttempts, b.dataNextAttemptAt = message, b.DataAttempts+1, next
	b.DataLeaseToken, b.dataLeaseUntil = "", time.Time{}
	if failed {
		b.DataState = EnvironmentBranchDataFailed
	}
	m.projectEnvironmentBranches[environmentID] = b
	return nil
}

func (m *MemStore) RecordProjectEnvironmentBranchDatabase(_ context.Context, copy ProjectEnvironmentBranchDatabase) (ProjectEnvironmentBranchDatabase, error) {
	if copy.AccountID == "" || copy.EnvironmentID == "" || copy.SourceDatabaseID == "" || copy.CopyName == "" || copy.PointInTime.IsZero() {
		return ProjectEnvironmentBranchDatabase{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.projectEnvironmentBranchDatabases {
		if existing.EnvironmentID == copy.EnvironmentID && existing.SourceDatabaseID == copy.SourceDatabaseID {
			return existing, nil
		}
		if existing.AccountID == copy.AccountID && existing.CopyName == copy.CopyName {
			return ProjectEnvironmentBranchDatabase{}, ErrConflict
		}
	}
	copy.ID, copy.CopyDatabaseID, copy.LeaseToken = newID(), "", ""
	copy.PointInTime, copy.CreatedAt = copy.PointInTime.UTC(), time.Now().UTC()
	m.projectEnvironmentBranchDatabases[copy.ID] = copy
	return copy, nil
}

func (m *MemStore) SetProjectEnvironmentBranchDatabaseCopy(_ context.Context, id, copyDatabaseID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.projectEnvironmentBranchDatabases[id]
	if !ok {
		return ErrNotFound
	}
	if row.CopyDatabaseID != "" && row.CopyDatabaseID != copyDatabaseID {
		return ErrConflict
	}
	row.CopyDatabaseID = copyDatabaseID
	m.projectEnvironmentBranchDatabases[id] = row
	return nil
}

func (m *MemStore) ListProjectEnvironmentBranchDatabases(_ context.Context, environmentID string) ([]ProjectEnvironmentBranchDatabase, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ProjectEnvironmentBranchDatabase
	for _, row := range m.projectEnvironmentBranchDatabases {
		if row.EnvironmentID == environmentID {
			out = append(out, row)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SourceDatabaseID < out[j].SourceDatabaseID })
	return out, nil
}

func (m *MemStore) ClaimOrphanedProjectEnvironmentBranchDatabase(_ context.Context, leaseToken string, now time.Time, lease time.Duration) (ProjectEnvironmentBranchDatabase, error) {
	if leaseToken == "" || lease <= 0 {
		return ProjectEnvironmentBranchDatabase{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var orphans []ProjectEnvironmentBranchDatabase
	for _, row := range m.projectEnvironmentBranchDatabases {
		if _, live := m.projectEnvironmentBranches[row.EnvironmentID]; !live && !row.leaseUntil.After(now) {
			orphans = append(orphans, row)
		}
	}
	if len(orphans) == 0 {
		return ProjectEnvironmentBranchDatabase{}, ErrNotFound
	}
	sort.Slice(orphans, func(i, j int) bool { return orphans[i].ID < orphans[j].ID })
	row := orphans[0]
	row.LeaseToken, row.leaseUntil = leaseToken, now.Add(lease)
	m.projectEnvironmentBranchDatabases[row.ID] = row
	return row, nil
}

func (m *MemStore) RemoveProjectEnvironmentBranchDatabase(_ context.Context, id, leaseToken string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.projectEnvironmentBranchDatabases[id]
	if !ok {
		return ErrNotFound
	}
	if row.LeaseToken != leaseToken {
		return ErrConflict
	}
	delete(m.projectEnvironmentBranchDatabases, id)
	return nil
}

func (m *MemStore) ProjectEnvironmentBranchDeploymentsAwaitingActivation(_ context.Context, environmentID string) ([]Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Deployment
	for id, row := range m.projectEnvironmentBranchDeployments {
		if row.EnvironmentID != environmentID {
			continue
		}
		if d, ok := m.deployments[id]; ok && d.Status == DeploySnapshotting {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *MemStore) EnvironmentBranchDataReady(_ context.Context, deploymentID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.projectEnvironmentBranchDeployments[deploymentID]
	if !ok {
		return true, nil
	}
	b, ok := m.projectEnvironmentBranches[row.EnvironmentID]
	return ok && b.DataState == EnvironmentBranchDataReady, nil
}
