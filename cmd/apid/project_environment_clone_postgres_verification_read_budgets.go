package main

import (
	"context"
	"errors"
	"reflect"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copycontents"
	"github.com/onebox-faas/faas/pkg/state"
)

// A retained debit selects its original quantities before host admission. A
// transient capacity refusal creates neither a new debit nor a native window.
func (s *server) admitClonePostgresVerificationRead(ctx context.Context, store state.ProjectEnvironmentClonePostgresVerificationReadBudgetStore, l state.ProjectEnvironmentCloneLease,
	original state.ProjectEnvironmentClonePostgresVerification, owner string, attempt int32, held *state.ProjectEnvironmentClonePostgresVerificationReadBudget, cfg copycontents.Config) (copycontents.Config, func(), error) {
	for _, a := range held.Allocations {
		if a.VerificationID == owner && a.Attempt == attempt {
			cfg.MaxBytes, cfg.SortMemoryBytes, cfg.SortDiskBytes = a.ReadBytes, int(a.SortMemoryBytes), a.SortDiskBytes
		}
	}
	cfg, release, err := s.reserveProjectEnvironmentClonePostgresRead(ctx, cfg)
	if err != nil {
		return copycontents.Config{}, nil, err
	}
	cfg, err = allocateClonePostgresVerificationRead(ctx, store, l, original, owner, attempt, held, cfg)
	if err != nil {
		release()
		return copycontents.Config{}, nil, err
	}
	return cfg, release, nil
}

// Only an undispatched original can create the aggregate hold. Legacy owners
// without a hold remain eligible for authenticated close-only recovery.
func clonePostgresVerificationReadBudget(ctx context.Context, store state.ProjectEnvironmentClonePostgresVerificationReadBudgetStore, l state.ProjectEnvironmentCloneLease, original state.ProjectEnvironmentClonePostgresVerification, cfg copycontents.Config, reserve bool) (state.ProjectEnvironmentClonePostgresVerificationReadBudget, error) {
	var zero state.ProjectEnvironmentClonePostgresVerificationReadBudget
	b, err := store.ProjectEnvironmentClonePostgresVerificationReadBudgetForLease(ctx, l, original.Scope.SourceDatabaseID, original.DatabaseOID)
	if errors.Is(err, state.ErrNotFound) {
		if !reserve || original.State != "reserved" {
			return zero, nil
		}
		read, memory, disk, err := cfg.ReadLimitsForWorker()
		if err != nil {
			return zero, err
		}
		b, _, err = store.ReserveProjectEnvironmentClonePostgresVerificationReadBudget(ctx, l, state.ProjectEnvironmentClonePostgresVerificationReadBudgetRequest{
			Scope: original.Scope, DatabaseOID: original.DatabaseOID, OriginalVerificationID: original.VerificationID, ReadBytes: read * api.PostgresCopyVerificationAttemptsMax, SortMemoryBytes: int64(memory), SortDiskBytes: disk},
			state.ProjectEnvironmentClonePostgresVerificationReadBudgetLimits{Count: api.PostgresCopyContentsManifestsPerAccountMax, Bytes: api.PostgresCopyVerificationReadBytesPerAccountMax})
		if err != nil {
			return zero, err
		}
	} else if err != nil {
		return zero, err
	}
	if !b.Scope.Equal(original.Scope) || b.DatabaseOID != original.DatabaseOID || b.OriginalVerificationID != original.VerificationID {
		return zero, managedpostgres.ErrConflict
	}
	return b, nil
}

func authorizeClonePostgresVerificationReadBudget(ctx context.Context, store state.ProjectEnvironmentClonePostgresVerificationReadBudgetStore, l state.ProjectEnvironmentCloneLease, original state.ProjectEnvironmentClonePostgresVerification, held state.ProjectEnvironmentClonePostgresVerificationReadBudget) error {
	fresh, err := clonePostgresVerificationReadBudget(ctx, store, l, original, copycontents.Config{}, false)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(fresh, held) {
		return managedpostgres.ErrConflict
	}
	return nil
}

// Called only by the native never-opened admission hook. A debit's complete
// planned maximum remains charged after uncertain dispatch. Lost responses
// recover its first quantities, rather than adopting current worker limits.
func allocateClonePostgresVerificationRead(ctx context.Context, store state.ProjectEnvironmentClonePostgresVerificationReadBudgetStore, l state.ProjectEnvironmentCloneLease, original state.ProjectEnvironmentClonePostgresVerification,
	owner string, attempt int32, held *state.ProjectEnvironmentClonePostgresVerificationReadBudget, cfg copycontents.Config) (copycontents.Config, error) {
	if held.OriginalVerificationID == "" {
		return copycontents.Config{}, managedpostgres.ErrConflict
	}
	for _, a := range held.Allocations {
		if a.VerificationID == owner && a.Attempt == attempt {
			cfg.MaxBytes, cfg.SortMemoryBytes, cfg.SortDiskBytes = a.ReadBytes, int(a.SortMemoryBytes), a.SortDiskBytes
			if _, _, _, err := cfg.ReadLimitsForWorker(); err != nil {
				return copycontents.Config{}, err
			}
			if err := authorizeClonePostgresVerificationReadBudget(ctx, store, l, original, *held); err != nil {
				return copycontents.Config{}, err
			}
			return cfg, nil
		}
	}
	read, memory, disk, err := cfg.ReadLimitsForWorker()
	if err != nil {
		return copycontents.Config{}, err
	}
	b, _, err := store.AllocateProjectEnvironmentClonePostgresVerificationRead(ctx, l, state.ProjectEnvironmentClonePostgresVerificationReadRequest{
		SourceDatabaseID: original.Scope.SourceDatabaseID, DatabaseOID: original.DatabaseOID, VerificationID: owner, Attempt: attempt, ReadBytes: read, SortMemoryBytes: int64(memory), SortDiskBytes: disk})
	if err != nil {
		// Native callbacks expose stable managed-PostgreSQL error kinds only.
		if errors.Is(err, state.ErrQuotaExceeded) {
			return copycontents.Config{}, managedpostgres.ErrQuotaExceeded
		}
		return copycontents.Config{}, err
	}
	// A successful reply must extend exactly the authenticated held prefix.
	if !b.Scope.Equal(held.Scope) || b.DatabaseOID != held.DatabaseOID || b.OriginalVerificationID != held.OriginalVerificationID || !b.CreatedAt.Equal(held.CreatedAt) ||
		b.ReadBytes != held.ReadBytes || b.SortMemoryBytes != held.SortMemoryBytes || b.SortDiskBytes != held.SortDiskBytes || len(b.Allocations) != len(held.Allocations)+1 ||
		!reflect.DeepEqual(b.Allocations[:len(held.Allocations)], held.Allocations) {
		return copycontents.Config{}, managedpostgres.ErrConflict
	}
	a := b.Allocations[len(b.Allocations)-1]
	if a.VerificationID != owner || a.Attempt != attempt || a.ReadBytes != read || a.SortMemoryBytes != int64(memory) || a.SortDiskBytes != disk || b.AllocatedReadBytes != held.AllocatedReadBytes+read {
		return copycontents.Config{}, managedpostgres.ErrConflict
	}
	*held = b
	cfg.MaxBytes, cfg.SortMemoryBytes, cfg.SortDiskBytes = a.ReadBytes, int(a.SortMemoryBytes), a.SortDiskBytes
	return cfg, nil
}
