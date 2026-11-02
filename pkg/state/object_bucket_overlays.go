package state

import (
	"context"
	"sort"
	"time"
)

// Environment branch buckets (ADR-20261031193635 slice 3). A branch's bucket
// is an ordinary bucket in the branch's scope with an overlay on its source:
// the S3 gateway serves a key the branch never wrote, and has not deleted,
// from the source bucket.

const (
	// ObjectBucketOverlayLiveParent reads the source's current object, so
	// source writes after the branch point show through.
	ObjectBucketOverlayLiveParent = "live_parent"
	// ObjectBucketOverlayAsOf reads the version current at AsOf from a
	// versioned source.
	ObjectBucketOverlayAsOf = "as_of"

	// ObjectBucketOverlayTombstoneBatch bounds one tombstone write or read.
	ObjectBucketOverlayTombstoneBatch = 1000
)

// ObjectBucketOverlay is one branch bucket's link to its source.
type ObjectBucketOverlay struct {
	BucketID       string
	AccountID      string
	SourceBucketID string
	SourceAppID    string
	Consistency    string
	AsOf           time.Time
	CreatedAt      time.Time
	// Source is the source bucket; zero when it no longer exists.
	Source ObjectBucket
}

// ObjectBucketOverlayStore is what the S3 gateway and apid need to serve
// and maintain overlays.
type ObjectBucketOverlayStore interface {
	// PutObjectBucketOverlay records (or returns) a bucket's overlay.
	PutObjectBucketOverlay(ctx context.Context, overlay ObjectBucketOverlay) (ObjectBucketOverlay, error)
	// ObjectBucketOverlay returns the bucket's overlay, ErrNotFound when the
	// bucket has none.
	ObjectBucketOverlay(ctx context.Context, bucketID string) (ObjectBucketOverlay, error)
	// AddObjectBucketOverlayTombstones records keys the branch deleted.
	AddObjectBucketOverlayTombstones(ctx context.Context, bucketID string, keys []string) error
	// ObjectBucketOverlayTombstones reports which of keys are tombstoned.
	ObjectBucketOverlayTombstones(ctx context.Context, bucketID string, keys []string) (map[string]bool, error)
	// DeleteObjectBucketOverlay drops a deleted bucket's overlay and its
	// tombstones. Deleting an absent overlay succeeds.
	DeleteObjectBucketOverlay(ctx context.Context, bucketID string) error
}

// ProjectEnvironmentBranchBucket is one overlay bucket a branch owns. The row
// exists before the bucket is reserved and outlives the branch until the
// bucket is purged and deleted.
type ProjectEnvironmentBranchBucket struct {
	ID              string
	AccountID       string
	ProjectID       string
	EnvironmentID   string
	EnvironmentSlug string
	AppID           string
	SourceBucketID  string
	CopyBucketID    string
	LeaseToken      string
	CreatedAt       time.Time
	leaseUntil      time.Time // MemStore bookkeeping
}

// ProjectEnvironmentBranchBucketStore tracks the overlay buckets branches own.
type ProjectEnvironmentBranchBucketStore interface {
	RecordProjectEnvironmentBranchBucket(ctx context.Context, bucket ProjectEnvironmentBranchBucket) (ProjectEnvironmentBranchBucket, error)
	SetProjectEnvironmentBranchBucketCopy(ctx context.Context, id, copyBucketID string) error
	ListProjectEnvironmentBranchBuckets(ctx context.Context, environmentID string) ([]ProjectEnvironmentBranchBucket, error)
	ClaimOrphanedProjectEnvironmentBranchBucket(ctx context.Context, leaseToken string, now time.Time, lease time.Duration) (ProjectEnvironmentBranchBucket, error)
	RemoveProjectEnvironmentBranchBucket(ctx context.Context, id, leaseToken string) error
}

func validOverlayTombstoneKeys(keys []string) bool {
	if len(keys) == 0 || len(keys) > ObjectBucketOverlayTombstoneBatch {
		return false
	}
	for _, key := range keys {
		if key == "" || len(key) > 1024 {
			return false
		}
	}
	return true
}

// MemStore

func (m *MemStore) PutObjectBucketOverlay(_ context.Context, overlay ObjectBucketOverlay) (ObjectBucketOverlay, error) {
	if overlay.BucketID == "" || overlay.AccountID == "" || overlay.SourceBucketID == "" || overlay.SourceAppID == "" ||
		overlay.SourceBucketID == overlay.BucketID || overlay.AsOf.IsZero() ||
		overlay.Consistency != ObjectBucketOverlayLiveParent && overlay.Consistency != ObjectBucketOverlayAsOf {
		return ObjectBucketOverlay{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if bucket, ok := m.objectBuckets[overlay.BucketID]; !ok || bucket.AccountID != overlay.AccountID {
		return ObjectBucketOverlay{}, ErrNotFound
	}
	if existing, ok := m.objectBucketOverlays[overlay.BucketID]; ok {
		if existing.SourceBucketID != overlay.SourceBucketID || existing.Consistency != overlay.Consistency {
			return ObjectBucketOverlay{}, ErrConflict
		}
		return m.overlayViewLocked(existing), nil
	}
	overlay.AsOf, overlay.CreatedAt, overlay.Source = overlay.AsOf.UTC(), time.Now().UTC(), ObjectBucket{}
	m.objectBucketOverlays[overlay.BucketID] = overlay
	return m.overlayViewLocked(overlay), nil
}

func (m *MemStore) overlayViewLocked(o ObjectBucketOverlay) ObjectBucketOverlay {
	if source, ok := m.objectBuckets[o.SourceBucketID]; ok && source.AccountID == o.AccountID && source.AppID == o.SourceAppID && source.State != "deleted" {
		o.Source = source
	}
	return o
}

func (m *MemStore) ObjectBucketOverlay(_ context.Context, bucketID string) (ObjectBucketOverlay, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	overlay, ok := m.objectBucketOverlays[bucketID]
	if !ok {
		return ObjectBucketOverlay{}, ErrNotFound
	}
	if bucket, live := m.objectBuckets[bucketID]; !live || bucket.State == "deleted" {
		return ObjectBucketOverlay{}, ErrNotFound
	}
	return m.overlayViewLocked(overlay), nil
}

func (m *MemStore) AddObjectBucketOverlayTombstones(_ context.Context, bucketID string, keys []string) error {
	if !validOverlayTombstoneKeys(keys) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.objectBucketOverlays[bucketID]; !ok {
		return ErrNotFound
	}
	if m.objectBucketOverlayTombstones[bucketID] == nil {
		m.objectBucketOverlayTombstones[bucketID] = map[string]bool{}
	}
	for _, key := range keys {
		m.objectBucketOverlayTombstones[bucketID][key] = true
	}
	return nil
}

func (m *MemStore) ObjectBucketOverlayTombstones(_ context.Context, bucketID string, keys []string) (map[string]bool, error) {
	if len(keys) == 0 {
		return map[string]bool{}, nil
	}
	if !validOverlayTombstoneKeys(keys) {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]bool{}
	for _, key := range keys {
		if m.objectBucketOverlayTombstones[bucketID][key] {
			out[key] = true
		}
	}
	return out, nil
}

func (m *MemStore) DeleteObjectBucketOverlay(_ context.Context, bucketID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objectBucketOverlays, bucketID)
	delete(m.objectBucketOverlayTombstones, bucketID)
	return nil
}

func (m *MemStore) RecordProjectEnvironmentBranchBucket(_ context.Context, bucket ProjectEnvironmentBranchBucket) (ProjectEnvironmentBranchBucket, error) {
	if bucket.AccountID == "" || bucket.EnvironmentID == "" || bucket.EnvironmentSlug == "" || bucket.AppID == "" || bucket.SourceBucketID == "" {
		return ProjectEnvironmentBranchBucket{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.projectEnvironmentBranchBuckets {
		if existing.EnvironmentID == bucket.EnvironmentID && existing.AppID == bucket.AppID && existing.SourceBucketID == bucket.SourceBucketID {
			return existing, nil
		}
	}
	bucket.ID, bucket.CopyBucketID, bucket.LeaseToken, bucket.CreatedAt = newID(), "", "", time.Now().UTC()
	m.projectEnvironmentBranchBuckets[bucket.ID] = bucket
	return bucket, nil
}

func (m *MemStore) SetProjectEnvironmentBranchBucketCopy(_ context.Context, id, copyBucketID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.projectEnvironmentBranchBuckets[id]
	if !ok {
		return ErrNotFound
	}
	if copyBucketID == "" || row.CopyBucketID != "" && row.CopyBucketID != copyBucketID {
		return ErrConflict
	}
	row.CopyBucketID = copyBucketID
	m.projectEnvironmentBranchBuckets[id] = row
	return nil
}

func (m *MemStore) ListProjectEnvironmentBranchBuckets(_ context.Context, environmentID string) ([]ProjectEnvironmentBranchBucket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ProjectEnvironmentBranchBucket
	for _, row := range m.projectEnvironmentBranchBuckets {
		if row.EnvironmentID == environmentID {
			out = append(out, row)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AppID != out[j].AppID {
			return out[i].AppID < out[j].AppID
		}
		return out[i].SourceBucketID < out[j].SourceBucketID
	})
	return out, nil
}

func (m *MemStore) ClaimOrphanedProjectEnvironmentBranchBucket(_ context.Context, leaseToken string, now time.Time, lease time.Duration) (ProjectEnvironmentBranchBucket, error) {
	if leaseToken == "" || lease <= 0 {
		return ProjectEnvironmentBranchBucket{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var orphans []ProjectEnvironmentBranchBucket
	for _, row := range m.projectEnvironmentBranchBuckets {
		if _, live := m.projectEnvironmentBranches[row.EnvironmentID]; !live && !row.leaseUntil.After(now) {
			orphans = append(orphans, row)
		}
	}
	if len(orphans) == 0 {
		return ProjectEnvironmentBranchBucket{}, ErrNotFound
	}
	sort.Slice(orphans, func(i, j int) bool { return orphans[i].ID < orphans[j].ID })
	row := orphans[0]
	row.LeaseToken, row.leaseUntil = leaseToken, now.Add(lease)
	m.projectEnvironmentBranchBuckets[row.ID] = row
	return row, nil
}

func (m *MemStore) RemoveProjectEnvironmentBranchBucket(_ context.Context, id, leaseToken string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.projectEnvironmentBranchBuckets[id]
	if !ok {
		return ErrNotFound
	}
	if row.LeaseToken != leaseToken {
		return ErrConflict
	}
	delete(m.projectEnvironmentBranchBuckets, id)
	return nil
}
