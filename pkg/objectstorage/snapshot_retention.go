package objectstorage

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ObjectVersionRetention is observed provider protection for the exact bytes
// selected by a manifest. It does not assert metadata immutability or a write
// barrier. RetainedUntil must not be reducible by a source writer or operator.
type ObjectVersionRetention struct {
	VersionID, MetadataVersion string
	RetainedUntil              time.Time
}

// ObjectVersionRetentionObserver reads existing protection. Implementations
// must reject removable holds and retention modes that can be bypassed. Clone
// workers never create an irreversible retention policy on a customer's source.
type ObjectVersionRetentionObserver interface {
	ObserveObjectVersionRetention(context.Context, string, ObjectVersion) (ObjectVersionRetention, error)
}

var ErrObjectSnapshotRetentionUnavailable = errors.New("object storage: pinned version retention is unavailable")

// VerifyObjectManifestRetention checks all versions before a manifest is
// committed or uncopied bytes are consumed. A new worker lease requires fresh
// observations; a stored version ID or old successful check is insufficient.
func VerifyObjectManifestRetention(ctx context.Context, provider ObjectVersionRetentionObserver, bucket string, manifest []ObjectVersion, until time.Time) error {
	if provider == nil || bucket == "" || until.IsZero() {
		return ErrInvalid
	}
	if _, err := ObjectManifestHash(manifest); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !until.After(time.Now()) {
		return ErrObjectSnapshotRetentionUnavailable
	}
	for _, item := range manifest {
		if err := ctx.Err(); err != nil {
			return err
		}
		observed, err := provider.ObserveObjectVersionRetention(ctx, bucket, item)
		if err != nil {
			return fmt.Errorf("observe pinned object retention: %w", err)
		}
		if observed.VersionID != item.VersionID || observed.MetadataVersion != item.MetadataVersion || observed.RetainedUntil.Before(until) {
			return ErrObjectSnapshotRetentionUnavailable
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !until.After(time.Now()) {
		return ErrObjectSnapshotRetentionUnavailable
	}
	return nil
}

func validRetentionObject(item ObjectVersion) bool {
	return ValidKey(item.Key) && item.VersionID != "" && item.Size >= 0 && !item.LastModified.IsZero() && !item.Deleted
}
