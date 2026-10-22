package storage

import (
	"context"
	"errors"
)

// ExclusiveArtifactCohortRetirer handles a capture's four original objects as
// one ownership unit. Local filesystems need this stronger primitive: four
// independent path deletes cannot condition each unlink on an inode receipt.
// Backends with a true per-object conditional delete may continue using
// ExclusiveArtifactRetirer instead.
type ExclusiveArtifactCohortRetirer interface {
	CheckExclusiveArtifactCohortRetirement(context.Context, string, [4]ExclusiveArtifactReceipt) error
	RetireExclusiveArtifactCohort(context.Context, string, [4]ExclusiveArtifactReceipt) error
}

func CheckExclusiveArtifactCohortRetirement(ctx context.Context, backend StorageBackend, captureID string, objects [4]ExclusiveArtifactReceipt) error {
	if err := validateExclusiveArtifactCohort(ctx, captureID, objects); err != nil {
		return err
	}
	if cohort, ok := backend.(ExclusiveArtifactCohortRetirer); ok {
		return cohort.CheckExclusiveArtifactCohortRetirement(ctx, captureID, objects)
	}
	for _, object := range objects {
		if err := CheckExclusiveArtifactRetirement(ctx, backend, object); err != nil {
			return err
		}
	}
	return nil
}

func RetireExclusiveArtifactCohort(ctx context.Context, backend StorageBackend, captureID string, objects [4]ExclusiveArtifactReceipt) error {
	if err := validateExclusiveArtifactCohort(ctx, captureID, objects); err != nil {
		return err
	}
	if cohort, ok := backend.(ExclusiveArtifactCohortRetirer); ok {
		return cohort.RetireExclusiveArtifactCohort(ctx, captureID, objects)
	}
	for _, object := range objects {
		if err := RetireExclusiveArtifact(ctx, backend, object); err != nil {
			return err
		}
	}
	return nil
}

func validateExclusiveArtifactCohort(ctx context.Context, captureID string, objects [4]ExclusiveArtifactReceipt) error {
	if captureID == "" {
		return ErrArtifactReceiptMismatch
	}
	for _, object := range objects {
		if err := errors.Join(object.Validate(), ctx.Err()); err != nil {
			return err
		}
	}
	return nil
}
