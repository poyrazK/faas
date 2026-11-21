package state

import (
	"context"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runenv"
	"sort"
	"time"
)

func (m *MemStore) CreateExecutionEnvironment(_ context.Context, row ExecutionEnvironment) (ExecutionEnvironment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := validateExecutionEnvironment(row); err != nil {
		return ExecutionEnvironment{}, err
	}
	acct, ok := m.accounts[row.AccountID]
	if !ok || !acct.Active() {
		return ExecutionEnvironment{}, ErrNotFound
	}
	count := 0
	for _, old := range m.executionEnvironments {
		if old.AccountID != row.AccountID || old.Status == "deleted" {
			continue
		}
		if old.PrincipalID == row.PrincipalID && old.RecipeDigest == row.RecipeDigest {
			return cloneExecutionEnvironment(old), nil
		}
		count++
	}
	if count >= api.ExecutionEnvironmentCountByPlan[acct.Plan.PlanIndex()] {
		return ExecutionEnvironment{}, &ExecutionEnvironmentQuotaError{Limit: api.ExecutionEnvironmentCountByPlan[acct.Plan.PlanIndex()], Observed: count + 1}
	}
	if m.executionEnvironments == nil {
		m.executionEnvironments = map[string]ExecutionEnvironment{}
	}
	row.Recipe = runenv.Canonical(row.Recipe)
	row.Status = environmentWorkerStatus("queued", row.Recipe)
	row.DiagnosticSequence = 0
	row.Diagnostics = nil
	row.CreatedAt = time.Now().UTC()
	m.executionEnvironments[row.ID] = row
	return cloneExecutionEnvironment(row), nil
}
func (m *MemStore) ExecutionEnvironmentByID(_ context.Context, account, id string) (ExecutionEnvironment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.executionEnvironmentLocked(account, id)
}
func (m *MemStore) executionEnvironmentLocked(account, id string) (ExecutionEnvironment, error) {
	row, ok := m.executionEnvironments[id]
	if !ok || row.AccountID != account || row.Status == "deleted" {
		return ExecutionEnvironment{}, ErrNotFound
	}
	return cloneExecutionEnvironment(row), nil
}
func (m *MemStore) ListExecutionEnvironments(_ context.Context, account, principal string, wide bool) ([]ExecutionEnvironment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []ExecutionEnvironment{}
	for _, row := range m.executionEnvironments {
		if row.AccountID == account && row.Status != "deleted" && (wide || row.PrincipalID == principal) {
			out = append(out, cloneExecutionEnvironment(row))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}
func (m *MemStore) RetireExecutionEnvironment(_ context.Context, account, id, principal string, wide bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, err := m.executionEnvironmentLocked(account, id)
	if err != nil || !wide && row.PrincipalID != principal {
		return ErrNotFound
	}
	row.Status = "retiring"
	m.executionEnvironments[id] = row
	return nil
}
func (m *MemStore) ClaimExecutionEnvironment(_ context.Context, phase string) (ExecutionEnvironment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	var selected *ExecutionEnvironment
	for _, row := range m.executionEnvironments {
		if !row.LeaseExpiresAt.IsZero() && !row.LeaseExpiresAt.After(now) && (runenv.PublicStatus(row.Status) == "building" && row.BuildAttempts >= api.ExecutionEnvironmentBuildMaxAttempts || runenv.PublicStatus(row.Status) == "imaging" && row.ImageAttempts >= api.ExecutionEnvironmentBuildMaxAttempts) {
			row.Status = "failed"
			row.FailureCode = "worker_recovery_exhausted"
			row.LeaseExpiresAt = time.Time{}
			m.executionEnvironments[row.ID] = row
		}
		acct, ok := m.accounts[row.AccountID]
		if !ok || !acct.Active() {
			continue
		}
		eligible := phase == "build" && (runenv.PublicStatus(row.Status) == "queued" || runenv.PublicStatus(row.Status) == "building" && !row.LeaseExpiresAt.After(now)) && row.BuildAttempts < api.ExecutionEnvironmentBuildMaxAttempts || phase == "image" && (runenv.PublicStatus(row.Status) == "built" || runenv.PublicStatus(row.Status) == "imaging" && !row.LeaseExpiresAt.After(now)) && row.ImageAttempts < api.ExecutionEnvironmentBuildMaxAttempts
		if eligible && (selected == nil || row.CreatedAt.Before(selected.CreatedAt)) {
			copy := row
			selected = &copy
		}
	}
	if selected == nil {
		return ExecutionEnvironment{}, ErrNotFound
	}
	row := *selected
	row.Lease = uuid.NewString()
	row.LeaseExpiresAt = now.Add(api.ExecutionEnvironmentLeaseSeconds * time.Second)
	if phase == "build" {
		row.Status = environmentWorkerStatus("building", row.Recipe)
		row.BuildAttempts++
		row.ExportKey = runenv.ArtifactKey(row.AccountID, row.ID, row.Lease, "image.tar")
		row.ArtifactKeys = append(row.ArtifactKeys, row.ExportKey)
	} else {
		row.Status = environmentWorkerStatus("imaging", row.Recipe)
		row.ImageAttempts++
		row.BaseKey = runenv.ArtifactKey(row.AccountID, row.ID, row.Lease, "base.ext4")
		row.ArtifactKeys = append(row.ArtifactKeys, row.BaseKey, ExecutionEnvironmentLayerKey(row), row.BaseKey+".digest", row.BaseKey+".content", "scans/"+row.BaseKey+".scan.json")
	}
	m.executionEnvironments[row.ID] = row
	return cloneExecutionEnvironment(row), nil
}
func (m *MemStore) PublishExecutionEnvironmentBuild(_ context.Context, claim ExecutionEnvironment, digest string) error {
	if !validEnvironmentDigest(digest) {
		return ErrExecutionInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row := m.executionEnvironments[claim.ID]
	if runenv.PublicStatus(row.Status) != "building" || row.Lease != claim.Lease || !row.LeaseExpiresAt.After(time.Now()) {
		return ErrExecutionEnvironmentLease
	}
	row.Status = environmentWorkerStatus("built", row.Recipe)
	row.ExportDigest = digest
	row.LeaseExpiresAt = time.Time{}
	m.executionEnvironments[row.ID] = row
	return nil
}
func (m *MemStore) PublishExecutionEnvironmentImage(_ context.Context, claim ExecutionEnvironment, digest, executorDigest string, size int64) error {
	if !validEnvironmentDigest(digest) || !validEnvironmentDigest(executorDigest) || size <= 0 || size > api.ExecutionEnvironmentMaxArchiveBytes {
		return ErrExecutionInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row := m.executionEnvironments[claim.ID]
	if runenv.PublicStatus(row.Status) != "imaging" || row.Lease != claim.Lease || !row.LeaseExpiresAt.After(time.Now()) {
		return ErrExecutionEnvironmentLease
	}
	row.Status = "ready"
	row.GuestExecutorDigest = executorDigest
	row.ImageDigest = digest
	row.ArtifactBytes = size
	row.LeaseExpiresAt = time.Time{}
	m.executionEnvironments[row.ID] = row
	return nil
}
func (m *MemStore) FailExecutionEnvironment(_ context.Context, claim ExecutionEnvironment, code string) error {
	if !validEnvironmentFailure(code) {
		return ErrExecutionInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row := m.executionEnvironments[claim.ID]
	if row.Lease != claim.Lease || row.Status != claim.Status || !row.LeaseExpiresAt.After(time.Now()) {
		return ErrExecutionEnvironmentLease
	}
	row.Status = "failed"
	row.FailureCode = code
	row.LeaseExpiresAt = time.Time{}
	m.executionEnvironments[row.ID] = row
	return nil
}
func (m *MemStore) CollectableExecutionEnvironments(_ context.Context) ([]ExecutionEnvironment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []ExecutionEnvironment{}
	for _, row := range m.executionEnvironments {
		_, exists := m.accounts[row.AccountID]
		if row.Status == "deleted" || exists && row.Status != "retiring" || row.LeaseExpiresAt.After(time.Now()) {
			continue
		}
		active := false
		for _, run := range m.executions {
			if run.EnvironmentID == row.ID && !run.Status.Terminal() {
				active = true
				break
			}
		}
		if !active {
			row.Status = "retiring"
			m.executionEnvironments[row.ID] = row
			out = append(out, cloneExecutionEnvironment(row))
			if len(out) >= api.ExecutionEnvironmentGCBatch {
				break
			}
		}
	}
	return out, nil
}
func (m *MemStore) DeleteExecutionEnvironmentArtifacts(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row := m.executionEnvironments[id]
	if row.Status != "retiring" || row.LeaseExpiresAt.After(time.Now()) {
		return ErrExecutionEnvironmentLease
	}
	for _, run := range m.executions {
		if run.EnvironmentID == id && !run.Status.Terminal() {
			return ErrExecutionEnvironmentLease
		}
	}
	row.Status = "deleted"
	row.Diagnostics = nil
	row.DiagnosticSequence = 0
	row.Recipe = api.CreateExecutionEnvironmentRequest{}
	row.ArtifactBytes = 0
	m.executionEnvironments[id] = row
	return nil
}
