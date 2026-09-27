package state

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// JobClaimLegacyArtifactVerification mirrors PgStore's separate, leased
// verification queue. No ordinary pending OCI job is eligible here.
func (m *MemStore) JobClaimLegacyArtifactVerification(_ context.Context, limit int, owner string, lease time.Duration) ([]Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 64
	}
	if owner == "" {
		owner = "imaged"
	}
	if lease <= 0 {
		lease = 15 * time.Minute
	}
	now := time.Now().UTC()
	var eligible []Job
	for _, job := range m.jobs {
		if job.Status == "deleted" || job.ImageMaterializationStatus != "verifying_legacy" || job.ImageStorageKey != job.ImageRef {
			continue
		}
		if job.ImageMaterializationNextAttemptAt != nil && job.ImageMaterializationNextAttemptAt.After(now) {
			continue
		}
		if claim, ok := m.jobMaterializationClaims[job.ID]; ok && claim.leaseUntil.After(now) {
			continue
		}
		eligible = append(eligible, job)
	}
	sort.Slice(eligible, func(i, j int) bool {
		if eligible[i].UpdatedAt.Equal(eligible[j].UpdatedAt) {
			return eligible[i].ID < eligible[j].ID
		}
		return eligible[i].UpdatedAt.Before(eligible[j].UpdatedAt)
	})
	if len(eligible) > limit {
		eligible = eligible[:limit]
	}
	for i, job := range eligible {
		job.ImageMaterializationAttempts++
		job.UpdatedAt = now
		m.jobs[job.ID] = job
		m.jobMaterializationClaims[job.ID] = jobMaterializationClaim{owner: owner, leaseUntil: now.Add(lease)}
		eligible[i] = job
	}
	return eligible, nil
}

func (m *MemStore) JobFinishLegacyArtifactVerification(_ context.Context, id, sourceRef, owner, promotedKey string, found bool, reason string) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if found && promotedKey != "jobs/"+id+".ext4" {
		return Job{}, fmt.Errorf("state: legacy job artifact requires its job-owned key")
	}
	if !found && promotedKey != "" {
		return Job{}, fmt.Errorf("state: missing legacy job artifact cannot have a promoted key")
	}
	if !found && reason == "" {
		return Job{}, fmt.Errorf("state: missing legacy job artifact requires a reason")
	}
	job, err := m.legacyArtifactVerificationClaimLocked(id, sourceRef, owner)
	if err != nil {
		return Job{}, err
	}
	now := time.Now().UTC()
	job.ImageMaterializationNextAttemptAt = nil
	job.ImageMaterializationError = ""
	job.ImageMaterializedAt = nil
	if found {
		job.ImageMaterializationStatus = "ready"
		job.ImageStorageKey = promotedKey
		job.ImageMaterializedAt = &now
	} else {
		job.ImageMaterializationStatus = "failed"
		job.ImageStorageKey = ""
		job.ImageMaterializationError = reason
	}
	job.UpdatedAt = now
	m.jobs[id] = job
	delete(m.jobMaterializationClaims, id)
	if !found {
		m.settleJobImageFailureLocked(id, reason)
	}
	return job, nil
}

func (m *MemStore) JobRetryLegacyArtifactVerification(_ context.Context, id, sourceRef, owner, reason string, retryAt time.Time) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if reason == "" {
		return Job{}, fmt.Errorf("state: legacy job artifact retry requires a reason")
	}
	job, err := m.legacyArtifactVerificationClaimLocked(id, sourceRef, owner)
	if err != nil {
		return Job{}, err
	}
	next := retryAt.UTC()
	job.ImageMaterializationError = reason
	job.ImageMaterializationNextAttemptAt = &next
	job.UpdatedAt = time.Now().UTC()
	m.jobs[id] = job
	delete(m.jobMaterializationClaims, id)
	return job, nil
}

func (m *MemStore) legacyArtifactVerificationClaimLocked(id, sourceRef, owner string) (Job, error) {
	job, ok := m.jobs[id]
	if !ok || job.Status == "deleted" {
		return Job{}, ErrNotFound
	}
	claim, claimed := m.jobMaterializationClaims[id]
	if job.ImageRef != sourceRef || job.ImageStorageKey != sourceRef || job.ImageMaterializationStatus != "verifying_legacy" || !claimed || claim.owner != owner || !claim.leaseUntil.After(time.Now()) {
		return Job{}, ErrConflict
	}
	return job, nil
}
