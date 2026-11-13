package object

import (
	"context"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// startAllVersionAccounting starts a newly created, still provisioning bucket
// in all-version accounting when its provider reports never-versioned
// buckets as Suspended (GCS). Otherwise the first write and the first delete
// each convert it, fencing writes for minutes and deletes for most of a
// minute (ADR-20261107214517). A failure is logged and leaves the bucket on
// that slower path; it never fails the creation.
func (s *Service) startAllVersionAccounting(ctx context.Context, p objectstorage.Provider, b state.ObjectBucket) {
	if !objectstorage.StartsInAllVersionAccounting(p) {
		return
	}
	st, ok := s.Host.Store().(state.ObjectBucketAllVersionStartStore)
	if !ok {
		return
	}
	started, err := st.StartObjectBucketAllVersionAccounting(ctx, b.AccountID, b.ID)
	if err != nil {
		s.Host.Logger().Warn("object storage: bucket did not start in all-version accounting", "bucket_id", b.ID, "backend_id", b.BackendID, "error", err)
		return
	}
	if !started {
		s.Host.Logger().Info("object storage: bucket already has accounting; all-version start skipped", "bucket_id", b.ID)
	}
}
