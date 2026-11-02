package object

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// advanceObjectInventory runs one attempt of a bucket's listing, which may
// continue an earlier attempt's (ADR-20261030100835). Only an attempt that
// starts a listing or publishes a complete one waits for the bucket's
// deletions: listing needs no quiet bucket, publication does.
func (s *Service) advanceObjectInventory(ctx context.Context, st state.ObjectStorageAccountingStore, scans state.ObjectInventoryScanStore, b state.ObjectBucket, report func(string), page func()) error {
	token := uuid.NewString()
	scan, err := scans.ClaimObjectInventoryScan(ctx, b.ID, token)
	if errors.Is(err, state.ErrConflict) {
		return nil
	}
	if err != nil {
		return err
	}
	claimed := scan
	if scan.Pages == 0 || scan.Complete {
		err = awaitObjectDeletions(ctx, st, b, api.ObjectInventoryDeletionDrain)
	}
	if err == nil && !scan.Complete {
		err = s.listObjectInventory(ctx, b, &scan, page)
	}
	if err == nil && scan.Complete {
		if err = scans.FinishObjectInventoryScan(ctx, b.ID, token, scan); err == nil {
			report("success")
			return nil
		}
	}
	outcome := "partial"
	if err != nil {
		outcome = "failed"
		s.Host.Logger().Warn("object storage inventory failed", "bucket_id", b.ID, "backend_id", b.BackendID, "pages", scan.Pages)
	}
	advanced := scan.Pages > claimed.Pages || scan.Complete && !claimed.Complete
	if !advanced || !checkpointObjectInventory(ctx, scans, b.ID, token, scan) {
		releaseObjectInventory(ctx, st, b.ID, token)
	}
	report(outcome)
	return nil
}

// checkpointObjectInventory records a listing's progress, which also releases
// its claim, even when the attempt's context has ended.
func checkpointObjectInventory(ctx context.Context, scans state.ObjectInventoryScanStore, bucket, token string, scan state.ObjectInventoryScan) bool {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return scans.CheckpointObjectInventoryScan(ctx, bucket, token, scan) == nil
}

// listObjectInventory advances scan for at most
// ObjectCapacityInventoryTimeout. Owned nodes, and providers that cannot list
// from a key, list the whole bucket in one attempt as before.
func (s *Service) listObjectInventory(ctx context.Context, b state.ObjectBucket, scan *state.ObjectInventoryScan, page func()) error {
	ctx, cancel := context.WithTimeout(ctx, api.ObjectCapacityInventoryTimeout)
	defer cancel()
	backend, err := s.Host.ObjectStorage().Resolve(b.BackendID, b.BackendFingerprint)
	if err != nil {
		return err
	}
	var record func(context.Context) error
	if page != nil {
		record = func(context.Context) error { page(); return nil }
	}
	if lister, ok := backend.Provider.(objectstorage.ObjectV2Lister); ok && !objectstorage.OwnedInventory(backend.Provider) {
		return ResumeObjectInventory(ctx, lister, b.PhysicalName, scan, record)
	}
	if scan.Pages != 0 {
		return objectstorage.ErrUnsupported
	}
	bytes, objects, err := completeBucketInventory(ctx, backend.Provider, b.PhysicalName, record)
	if err != nil {
		return err
	}
	scan.Bytes, scan.Keys, scan.Pages, scan.Complete = bytes, objects, 1, true
	return nil
}

// ResumeObjectInventory lists a bucket after scan's last key, adding each
// validated page to scan, until the listing ends (scan.Complete), ctx ends or
// the provider fails (ADR-20261030100835). Keys must ascend across attempts.
// A page is added whole or not at all, and pages already added stay in scan
// when it returns an error. Running out of time is not an error.
func ResumeObjectInventory(ctx context.Context, p objectstorage.ObjectV2Lister, physical string, scan *state.ObjectInventoryScan, record func(context.Context) error) error {
	after, cursor := scan.After, ""
	seen := map[string]bool{}
	for ctx.Err() == nil {
		if scan.Pages >= api.ObjectStorageInventoryMaxPages {
			return objectstorage.ErrUnavailable
		}
		if record != nil {
			if err := record(ctx); err != nil {
				return err
			}
		}
		page, err := p.ListObjectsV2(ctx, physical, objectstorage.ObjectListRequest{StartAfter: after, Cursor: cursor, Limit: api.MaxObjectS3ListItems})
		if err != nil {
			if ctx.Err() != nil {
				return nil //nolint:nilerr // intentional: the attempt's time ran out mid-page, and the next attempt lists it
			}
			return err
		}
		if len(page.Items) > api.MaxObjectS3ListItems || len(page.CommonPrefixes) != 0 || len(page.Items) == 0 && page.NextCursor != "" {
			return objectstorage.ErrInvalid
		}
		next := *scan
		for _, o := range page.Items {
			if !objectstorage.ValidKey(o.Key) || o.Key <= next.After || o.Size < 0 || o.Size > api.MaxObjectStoragePolicyValue-next.Bytes {
				return objectstorage.ErrInvalid
			}
			next.After, next.Bytes, next.Keys = o.Key, next.Bytes+o.Size, next.Keys+1
		}
		next.Pages++
		*scan = next
		if page.NextCursor == "" {
			scan.Complete = true
			return nil
		}
		if seen[page.NextCursor] || len(page.NextCursor) > 8192 {
			return objectstorage.ErrInvalid
		}
		seen[page.NextCursor] = true
		cursor = page.NextCursor
	}
	return nil
}
