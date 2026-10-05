package state

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
)

func (m *MemStore) ReserveOperationArtifact(_ context.Context, id string, authority OperationExecutionAuthority, req api.OperationArtifactRequest) (OperationResultBlob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationMemoryLocked()
	op, exists := data.operations[id]
	if !exists {
		return OperationResultBlob{}, ErrNotFound
	}
	inv, exists := m.invocations[authority.InvocationID]
	if !exists {
		return OperationResultBlob{}, ErrNotFound
	}
	now := time.Now().UTC()
	if err := ValidateOperationExecutionAuthority(op, inv, authority, now); err != nil {
		return OperationResultBlob{}, err
	}
	key := fmt.Sprintf("%s/%s/%d/%s", op.ID, inv.ID, inv.Attempts, req.ReportID)
	if prior, exists := data.reports[key]; exists {
		if prior != artifactFingerprint(req) {
			return OperationResultBlob{}, ErrOperationInputConflict
		}
		artifactID := operations.ArtifactIdentity(op.ID, inv.ID, inv.Attempts, req.ReportID)
		for _, blob := range data.blobs {
			if blob.StorageKey == op.ArtifactStorageKeys[artifactID] {
				return blob, nil
			}
		}
		return OperationResultBlob{}, ErrConflict
	}
	copy := cloneOperation(op)
	if _, err := operationArtifact(&copy, inv, req, now); err != nil {
		return OperationResultBlob{}, err
	}
	var count, bytes int64
	for _, blob := range data.blobs {
		if blob.AccountID == op.AccountID {
			count++
			bytes += blob.SizeBytes
		}
	}
	limits := api.MustLimitsFor(m.accounts[op.AccountID].Plan).Operations
	if err := checkOperationBlobQuota(limits, count, bytes, req.SizeBytes); err != nil {
		return OperationResultBlob{}, err
	}
	blob := newOperationResultBlob(op, inv, req, now)
	data.blobs[blob.ID] = blob
	return blob, nil
}

func (m *MemStore) ClaimOperationArtifactCleanup(_ context.Context, token string, now time.Time) (OperationResultBlob, error) {
	if token == "" {
		return OperationResultBlob{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationMemoryLocked()
	for id, blob := range data.blobs {
		if blob.NextAttemptAt.After(now) || (blob.LeaseUntil != nil && blob.LeaseUntil.After(now)) {
			continue
		}
		if blob.State == "staging" && blob.ExpiresAt.After(now) {
			continue
		}
		if blob.State == "retained" {
			op, exists := data.operations[blob.OperationID]
			if exists && (op.ExpiresAt.After(now) || op.State == api.OperationAccepted || op.State == api.OperationRunning) {
				pinned := false
				for _, key := range op.ArtifactStorageKeys {
					if key == blob.StorageKey {
						pinned = true
					}
				}
				if pinned {
					continue
				}
			}
		}
		expiry := now.Add(api.OperationArtifactCleanupLease)
		blob.State, blob.LeaseToken, blob.LeaseUntil = "deleting", token, &expiry
		data.blobs[id] = blob
		return blob, nil
	}
	return OperationResultBlob{}, ErrNotFound
}

func (m *MemStore) RetryOperationArtifactCleanup(_ context.Context, id, token string, next time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationMemoryLocked()
	blob, exists := data.blobs[id]
	if !exists || token == "" || blob.State != "deleting" || blob.LeaseToken != token || blob.LeaseUntil == nil || !blob.LeaseUntil.After(time.Now()) {
		return ErrConflict
	}
	blob.LeaseToken, blob.LeaseUntil, blob.NextAttemptAt = "", nil, next
	data.blobs[id] = blob
	return nil
}

func (m *MemStore) CompleteOperationArtifactCleanup(_ context.Context, id, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationMemoryLocked()
	blob, exists := data.blobs[id]
	if !exists || token == "" || blob.State != "deleting" || blob.LeaseToken != token || blob.LeaseUntil == nil || !blob.LeaseUntil.After(time.Now()) {
		return ErrConflict
	}
	delete(data.blobs, id)
	return nil
}

var _ OperationResultBlobStore = (*MemStore)(nil)
