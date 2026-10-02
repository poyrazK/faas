package logarchive

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/region"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Shipper) discardLocalCollection(f FileInfo) error {
	result, err := s.spool.discardCollectedDay(f.Instance, f.Day)
	if metrics, ok := s.metrics.(interface{ ObserveLocalCollection(int, int64) }); ok {
		metrics.ObserveLocalCollection(result.Files, result.PendingBytes)
	}
	if result.Files != 0 || result.PendingBytes != 0 {
		s.log.Info("logarchive.local_collected", "instance", f.Instance, "day", f.Day, "files", result.Files, "pending_bytes", result.PendingBytes)
	}
	return err
}

func (s *Shipper) collectExpiredLocalDay(ctx context.Context, f FileInfo, publicationErr error) error {
	reader, ok := s.catalog.(state.RuntimeLogArchiveCollectionReader)
	if !ok || s.locker == nil {
		return publicationErr
	}
	binding, err := s.catalog.RuntimeLogArchiveBindingByInstance(ctx, f.Instance)
	if err != nil {
		return fmt.Errorf("local collection binding: %w", err)
	}
	ref := state.RuntimeLogArchive{RuntimeLogArchiveBinding: binding, Day: f.Day, StorageKey: region.RuntimeLogArchiveKey(binding.ComputeRegion, f.Instance, f.Day)}
	if binding.InstanceID != f.Instance || !ref.ValidReference() {
		return publicationErr
	}
	unlock, err := s.locker.AcquireRuntimeLogArchivePublicationLock(ctx, binding.AppID)
	if err != nil {
		return err
	}
	defer unlock(context.WithoutCancel(ctx))
	collected, err := reader.RuntimeLogArchiveDayCollected(ctx, f.Instance, f.Day)
	if err != nil {
		return fmt.Errorf("local collection receipt: %w", err)
	}
	if !collected {
		return publicationErr
	}
	return s.discardLocalCollection(f)
}
