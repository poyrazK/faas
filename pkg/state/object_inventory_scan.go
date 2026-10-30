package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ObjectInventoryScan is a bucket's inventory listing in progress
// (ADR-20261030100835). It lists keys after After; Bytes, Keys and Pages count
// what it has listed so far, and Complete marks a listing that reached the end
// and waits to be published. A zero StartedAt means no listing.
type ObjectInventoryScan struct {
	StartedAt, ProgressAt time.Time
	After                 string
	Bytes, Keys           int64
	Pages                 int
	Complete              bool
}

// ObjectInventoryScanStore lists a bucket's inventory over several attempts,
// so a bucket too large to list in one attempt keeps a fresh inventory
// (ADR-20261030100835). Each attempt claims the bucket like
// ClaimObjectInventory and ends with a checkpoint, a publication or
// ReleaseObjectInventory.
type ObjectInventoryScanStore interface {
	// ClaimObjectInventoryScan claims the bucket and returns its listing: the
	// one in progress, or a new one when there is none it can continue. A
	// claim that starts a listing or publishes a complete one makes new
	// deletions in the bucket yield; a claim that continues listing does not.
	ClaimObjectInventoryScan(ctx context.Context, bucket, token string) (ObjectInventoryScan, error)
	// CheckpointObjectInventoryScan records the listing's progress and
	// releases the claim.
	CheckpointObjectInventoryScan(ctx context.Context, bucket, token string, scan ObjectInventoryScan) error
	// FinishObjectInventoryScan publishes a complete listing as observed at
	// its start and releases the claim. It refuses, keeping the bucket's
	// observation, when the listing started before that observation or
	// FinishObjectInventory would refuse.
	FinishObjectInventoryScan(ctx context.Context, bucket, token string, scan ObjectInventoryScan) error
}

// inventoryScanResumable reports a listing a claim can continue or publish: it
// listed at least one page, started after the bucket's observation and less
// than ObjectInventoryScanMaxAge ago, and is complete or within the page
// budget. Any other listing starts again.
func inventoryScanResumable(s ObjectInventoryScan, observedAt, now time.Time) bool {
	return s.Pages > 0 && s.StartedAt.After(observedAt) && !s.StartedAt.After(now) &&
		now.Sub(s.StartedAt) <= api.ObjectInventoryScanMaxAge &&
		(s.Complete || s.Pages < api.ObjectStorageInventoryMaxPages)
}

// validInventoryScanProgress bounds a listing a store records.
func validInventoryScanProgress(s ObjectInventoryScan) bool {
	return !s.StartedAt.IsZero() && s.Pages > 0 && s.Pages <= api.ObjectStorageInventoryMaxPages &&
		s.Bytes >= 0 && s.Keys >= 0 && s.Bytes <= api.MaxObjectStoragePolicyValue && s.Keys <= api.MaxObjectStoragePolicyValue &&
		len(s.After) <= 1024
}

// listedSoFar is what the bucket's current listing has seen, or nothing when
// its listing predates the observation.
func (u ObjectBucketUsage) listedSoFar() (bytes, keys int64) {
	if s := u.InventoryScan; s.Pages > 0 && s.StartedAt.After(u.ObservedAt) {
		return s.Bytes, s.Keys
	}
	return 0, 0
}

// objectBucketInventoryStale reports a bucket whose inventory admission cannot
// rely on: missing, or older than maxAge. A bucket whose listing advanced
// within ObjectInventoryScanProgressWindow stays fresh on an observation up to
// ObjectInventoryScanMaxAge old (ADR-20261030100835).
func objectBucketInventoryStale(u ObjectBucketUsage, maxAge time.Duration, now time.Time) bool {
	if !objectInventoryStale(u.ObservedAt, maxAge, now) {
		return false
	}
	s := u.InventoryScan
	progressing := s.Pages > 0 && s.StartedAt.After(u.ObservedAt) && !s.ProgressAt.After(now) &&
		now.Sub(s.ProgressAt) <= api.ObjectInventoryScanProgressWindow
	return !progressing || objectInventoryStale(u.ObservedAt, api.ObjectInventoryScanMaxAge, now)
}
