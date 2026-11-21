package state

import (
	"context"
	"crypto/subtle"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runfile"
)

var _ ExecutionFileStore = (*MemStore)(nil)

func (m *MemStore) ExecutionFileByID(_ context.Context, account, id string) (ExecutionFile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.executionFiles[id]
	if !ok || f.AccountID != account {
		return ExecutionFile{}, ErrNotFound
	}
	return cloneExecutionFile(f), nil
}
func (m *MemStore) executionFileUsageLocked(account string) (int64, int) {
	var size int64
	count := 0
	pending := map[string]bool{}
	for _, f := range m.executionFiles {
		if f.AccountID != account {
			continue
		}
		count++
		if f.Kind == "upload" || f.Status == "ready" {
			size += f.SizeBytes
		}
		if f.Kind == "output" && f.Status != "ready" && f.ExecutionID != nil {
			pending[*f.ExecutionID] = true
		}
	}
	for _, e := range m.executions {
		if e.AccountID == account && (!e.Status.Terminal() || pending[e.ID]) {
			size += e.Limits.MaxArtifactBytes
		}
	}
	return size, count
}
func (m *MemStore) ClaimExecutionFileUpload(_ context.Context, f ExecutionFile) (ExecutionFile, bool, error) {
	if err := validateExecutionFileUpload(f); err != nil {
		return ExecutionFile{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.executionFiles[f.ID]; ok {
		if old.AccountID != f.AccountID || !executionFileOwner(old, f.PrincipalID, false) || old.SizeBytes != f.SizeBytes || old.SHA256 != f.SHA256 {
			return ExecutionFile{}, false, ErrExecutionInvalid
		}
		if old.Status == "ready" && old.ExpiresAt.After(f.UpdatedAt) {
			return cloneExecutionFile(old), true, nil
		}
		return ExecutionFile{}, false, ErrExecutionFileBusy
	}
	acct, ok := m.accounts[f.AccountID]
	if !ok {
		return ExecutionFile{}, false, ErrNotFound
	}
	limits := acct.Plan.ExecutionFileLimits()
	if f.SizeBytes > limits.MaxFileBytes {
		return ExecutionFile{}, false, ErrExecutionInvalid
	}
	size, count := m.executionFileUsageLocked(f.AccountID)
	if err := validateExecutionFileQuota(limits, size+f.SizeBytes, count+1); err != nil {
		return ExecutionFile{}, false, err
	}
	if m.executionFiles == nil {
		m.executionFiles = map[string]ExecutionFile{}
	}
	m.executionFiles[f.ID] = cloneExecutionFile(f)
	return cloneExecutionFile(f), false, nil
}
func (m *MemStore) FinishExecutionFileUpload(_ context.Context, account, id, token string, at time.Time) (ExecutionFile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.executionFiles[id]
	if !ok || f.AccountID != account || f.Status != "uploading" || f.Token == nil || *f.Token != token || f.LeaseExpiresAt == nil || !f.LeaseExpiresAt.After(at) || !f.ExpiresAt.After(at) {
		return ExecutionFile{}, ErrExecutionFileLeaseLost
	}
	f.Status = "ready"
	f.Token = nil
	f.LeaseExpiresAt = nil
	f.UpdatedAt = at
	m.executionFiles[id] = f
	return cloneExecutionFile(f), nil
}
func (m *MemStore) FailExecutionFileUpload(_ context.Context, account, id, token string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.executionFiles[id]
	if ok && f.AccountID == account && f.Status == "uploading" && f.Token != nil && *f.Token == token {
		f.Status = "failed"
		f.UpdatedAt = at
		m.executionFiles[id] = f
	}
	return nil
}
func (m *MemStore) RevokeExecutionFile(_ context.Context, account, id string, principal *string, wide bool, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.executionFiles[id]
	if !ok || f.AccountID != account || !executionFileOwner(f, principal, wide) {
		return ErrNotFound
	}
	if f.ExpiresAt.After(at) {
		f.ExpiresAt = at
	}
	f.UpdatedAt = at
	m.executionFiles[id] = f
	return nil
}
func (m *MemStore) fileInUseLocked(f ExecutionFile) bool {
	if _, exists := m.accounts[f.AccountID]; !exists {
		return false
	}
	if f.ExecutionID != nil {
		if e, ok := m.executions[*f.ExecutionID]; ok && !e.Status.Terminal() {
			return true
		}
	}
	for id, files := range m.executionFileLinks {
		e, exists := m.executions[id]
		if !exists || e.Status.Terminal() {
			continue
		}
		for _, v := range files {
			if v == f.ID {
				return true
			}
		}
	}
	return false
}
func (m *MemStore) ClaimExpiredExecutionFiles(_ context.Context, at time.Time, limit int) ([]ExecutionFile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit < 1 || limit > api.ExecutionFileGCBatchSize {
		limit = api.ExecutionFileGCBatchSize
	}
	out := []ExecutionFile{}
	safe := at.Add(-api.ExecutionFileGCGrace)
	for id, f := range m.executionFiles {
		_, ownerExists := m.accounts[f.AccountID]
		expired := !ownerExists || !f.ExpiresAt.After(at) || f.Status == "failed" || f.Status == "deleting" || f.Status == "uploading" && f.LeaseExpiresAt != nil && !f.LeaseExpiresAt.After(at)
		if !expired || f.UpdatedAt.After(safe) || f.LeaseExpiresAt != nil && f.LeaseExpiresAt.After(safe) || m.fileInUseLocked(f) {
			continue
		}
		token := uuid.NewString()
		lease := at.Add(api.ExecutionFileUploadTimeout)
		f.Status = "deleting"
		f.Token = &token
		f.LeaseExpiresAt = &lease
		f.UpdatedAt = at
		m.executionFiles[id] = f
		out = append(out, cloneExecutionFile(f))
		if len(out) == limit {
			break
		}
	}
	return out, nil
}
func (m *MemStore) DeleteExecutionFileMetadata(_ context.Context, account, id, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.executionFiles[id]
	if !ok {
		return nil
	}
	if f.AccountID != account || f.Status != "deleting" || f.Token == nil || *f.Token != token || m.fileInUseLocked(f) {
		return ErrExecutionFileLeaseLost
	}
	delete(m.executionFiles, id)
	for e, files := range m.executionFileLinks {
		kept := files[:0]
		for _, v := range files {
			if v != id {
				kept = append(kept, v)
			}
		}
		m.executionFileLinks[e] = kept
	}
	return nil
}
func (m *MemStore) validateExecutionFilesLocked(p CreateExecutionParams, plan api.Plan) error {
	if err := validateStoredOutputs(p); err != nil {
		return err
	}
	limits := plan.ExecutionFileLimits()
	if p.Request.Limits.MaxArtifactBytes > limits.MaxOutputBytes || storedInputBytes(p.Request.StoredInputs) > limits.MaxInputBytes {
		return ErrExecutionInvalid
	}
	for _, ref := range p.Request.StoredInputs {
		f, ok := m.executionFiles[ref.ID]
		if !ok || !validAdmittedInput(f, ref, p) {
			return ErrExecutionFileNotReady
		}
		if !executionFileOwner(f, p.RunsPrincipalID, p.FilesAccountWide) && !m.fileGrantMatchesLocked(f, p) {
			return ErrExecutionArtifactGrantUnavailable
		}
	}
	for _, ref := range p.Request.StoredOutputs {
		if _, exists := m.executionFiles[ref.ID]; exists {
			return ErrExecutionInvalid
		}
	}
	size, count := m.executionFileUsageLocked(p.AccountID)
	return validateExecutionFileQuota(limits, size+p.Request.Limits.MaxArtifactBytes, count+len(p.Request.StoredOutputs))
}
func (m *MemStore) fileGrantMatchesLocked(f ExecutionFile, p CreateExecutionParams) bool {
	for _, r := range p.ArtifactGrantRedemptions {
		g, ok := m.executionArtifactGrants[r.GrantID]
		if !ok || g.AccountID != p.AccountID || g.RevokedAt != nil || g.RedeemedAt != nil || !g.ExpiresAt.After(p.AdmittedAt) || subtle.ConstantTimeCompare(g.TokenHash, r.TokenHash) != 1 || f.ExecutionID == nil || g.SourceExecutionID != *f.ExecutionID || g.ArtifactName != f.Name {
			continue
		}
		return true
	}
	return false
}
func validAdmittedInput(f ExecutionFile, ref runfile.Ref, p CreateExecutionParams) bool {
	return f.AccountID == p.AccountID && f.Status == "ready" && f.ExpiresAt.After(p.AdmittedAt) && ref.SizeBytes == f.SizeBytes && ref.SHA256 == f.SHA256 && ref.StorageKey == f.StorageKey && runfile.ValidPath(ref.Path, api.ExecutionBundleMaxPathSize) && ref.PrivateKey != ""
}
func (m *MemStore) addExecutionFilesLocked(p CreateExecutionParams, id string) {
	if m.executionFiles == nil {
		m.executionFiles = map[string]ExecutionFile{}
	}
	if m.executionFileLinks == nil {
		m.executionFileLinks = map[string][]string{}
	}
	for _, f := range outputExecutionFiles(p, id) {
		m.executionFiles[f.ID] = f
	}
	for _, f := range p.Request.StoredInputs {
		m.executionFileLinks[id] = append(m.executionFileLinks[id], f.ID)
	}
}
func (m *MemStore) completeExecutionFilesLocked(row Execution, p CompleteExecutionParams) error {
	files := []ExecutionFile{}
	for _, f := range m.executionFiles {
		if f.ExecutionID != nil && *f.ExecutionID == row.ID {
			files = append(files, f)
		}
	}
	if err := validateStoredCompletion(row, p, files); err != nil {
		return err
	}
	if p.Status == api.ExecutionStatusSucceeded {
		for _, a := range p.Artifacts {
			if a.FileID == "" {
				continue
			}
			f := m.executionFiles[a.FileID]
			f.Status = "ready"
			f.SizeBytes = int64(a.SizeBytes)
			f.SHA256 = a.SHA256
			f.LeaseExpiresAt = nil
			f.UpdatedAt = p.FinishedAt
			m.executionFiles[f.ID] = f
		}
	}
	return nil
}
func validateStoredCompletion(row Execution, p CompleteExecutionParams, files []ExecutionFile) error {
	stored := 0
	var size int64
	for _, a := range p.Artifacts {
		if a.FileID == "" {
			if len(files) > 0 {
				return ErrExecutionInvalidTerminal
			}
			continue
		}
		stored++
		size += int64(a.SizeBytes)
		found := false
		for _, f := range files {
			if f.ID == a.FileID && f.Name == a.Name && f.Status == "pending" {
				found = true
			}
		}
		if !found {
			return ErrExecutionInvalidTerminal
		}
	}
	if size > row.Limits.MaxArtifactBytes || p.Status == api.ExecutionStatusSucceeded && stored != len(files) {
		return ErrExecutionInvalidTerminal
	}
	return nil
}
