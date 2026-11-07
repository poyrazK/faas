package state

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

// EnvironmentBranchObjectRelayStore answers the bridge object relay
// (ADR-20261031193635 slice 6c): a contained branch guest may sign object
// requests only with a key bound to one of its own branch's overlay buckets,
// so a production key compiled into the build cannot reach a parent bucket.
type EnvironmentBranchObjectRelayStore interface {
	// EnvironmentBranchObjectCredentialAllowed reports whether accessKeyID
	// is an active credential of an overlay bucket the environment owns.
	// Malformed arguments are not an error; they are simply not allowed.
	EnvironmentBranchObjectCredentialAllowed(ctx context.Context, environmentID, accessKeyID string) (bool, error)
}

var _ EnvironmentBranchObjectRelayStore = (*MemStore)(nil)
var _ EnvironmentBranchObjectRelayStore = (*PgStore)(nil)

func (m *MemStore) EnvironmentBranchObjectCredentialAllowed(_ context.Context, environmentID, accessKeyID string) (bool, error) {
	accessKeyID = strings.TrimSpace(accessKeyID)
	if environmentID == "" || accessKeyID == "" {
		return false, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	buckets := make(map[string]bool)
	for _, row := range m.projectEnvironmentBranchBuckets {
		if row.EnvironmentID == environmentID && row.CopyBucketID != "" {
			buckets[row.CopyBucketID] = true
		}
	}
	for _, c := range m.objectS3Credentials {
		if c.AccessKeyID == accessKeyID && c.Status == ObjectS3CredentialStatusActive && buckets[c.BucketID] {
			return true, nil
		}
	}
	return false, nil
}

func (s *PgStore) EnvironmentBranchObjectCredentialAllowed(ctx context.Context, environmentID, accessKeyID string) (bool, error) {
	accessKeyID = strings.TrimSpace(accessKeyID)
	if _, err := uuid.Parse(environmentID); err != nil || accessKeyID == "" {
		return false, nil
	}
	var allowed bool
	err := s.pool.QueryRow(ctx, `
		select exists (
			select 1
			  from object_storage_s3_credentials c
			  join project_environment_branch_buckets b on b.copy_bucket_id = c.bucket_id
			 where b.environment_id = $1 and c.access_key_id = $2 and c.status = 'active')
	`, environmentID, accessKeyID).Scan(&allowed)
	if err != nil {
		return false, mapErr(err)
	}
	return allowed, nil
}
