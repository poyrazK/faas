package main

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copycontents"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/state"
)

type clonePostgresDataWorkConfig struct {
	Artifact                                   clonePostgresArchiveStorage
	PGDump, PGRestore, ScratchRoot             string
	MaxPlainBytes, ArchiveBytes, ContentsBytes int64
	ArchiveLimits                              state.ProjectEnvironmentClonePostgresArchiveLimits
	ContentsLimits                             state.ProjectEnvironmentClonePostgresContentsLimits
	Read                                       copycontents.Config
}

func (clonePostgresDataWorkConfig) String() string {
	return "private PostgreSQL copy worker configuration"
}
func (c clonePostgresDataWorkConfig) GoString() string           { return c.String() }
func (clonePostgresDataWorkConfig) MarshalJSON() ([]byte, error) { return json.Marshal(struct{}{}) }

type clonePostgresDataWorkProgress struct {
	Phase        string
	Verification state.ProjectEnvironmentClonePostgresVerificationAttempt
}

type clonePostgresDataWorkStore interface {
	state.ProjectEnvironmentClonePostgresArchiveStore
	state.ProjectEnvironmentClonePostgresContentsStore
	state.ProjectEnvironmentClonePostgresDatabaseSQLPinsStore
	state.ProjectEnvironmentClonePostgresImportStore
	state.ProjectEnvironmentClonePostgresVerificationStore
}

// One durable database work step. Later retained ownership takes precedence over
// earlier producer configuration: replay never exports today's source, restores
// an archive twice, or recaptures a compared manifest. This publishes subordinate
// data evidence only, not full database/global authority or stage readiness.
func (s *server) processProjectEnvironmentClonePostgresData(ctx context.Context, lease state.ProjectEnvironmentCloneLease,
	source capturedProjectEnvironmentDatabasePlan, exports copyinventory.ExportPlan, oid uint32, cfg clonePostgresDataWorkConfig) (clonePostgresDataWorkProgress, error) {
	var zero clonePostgresDataWorkProgress
	store, ok := s.store.(clonePostgresDataWorkStore)
	if !ok {
		return zero, managedpostgres.ErrUnavailable
	}
	ctx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
	defer cancel()
	requirements, err := exports.RequirementsForWorker()
	if err != nil {
		return zero, err
	}
	var selected copyinventory.DatabaseExport
	for _, d := range requirements {
		if d.Database.OID == oid {
			selected = d
		}
	}
	if selected.Database.OID == 0 || !clonePostgresInventoryMatchesSource(lease, source, selected.Scope) {
		return zero, managedpostgres.ErrConflict
	}
	id := source.source.ID
	verify := func() (clonePostgresDataWorkProgress, error) {
		verified, err := s.projectEnvironmentClonePostgresVerificationWithRetries(ctx, lease, source, exports, oid, cfg.Read)
		if err != nil {
			return zero, err
		}
		if verified.State != "verified" || verified.VerificationID == "" || verified.DatabaseOID != oid || !verified.Scope.Equal(selected.Scope) ||
			verified.Attempt < 1 || verified.Attempt > api.PostgresCopyVerificationAttemptsMax {
			return zero, managedpostgres.ErrConflict
		}
		return clonePostgresDataWorkProgress{Phase: "contents_verified", Verification: verified}, nil
	}
	_, err = store.ProjectEnvironmentClonePostgresVerificationForLease(ctx, lease, id, oid)
	if err == nil {
		return verify()
	}
	if !errors.Is(err, state.ErrNotFound) {
		return zero, err
	}
	imported, err := store.ProjectEnvironmentClonePostgresImportForLease(ctx, lease, id, oid)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return zero, err
	}
	if err == nil && imported.State != "reserved" {
		prepared, err := s.openProjectEnvironmentClonePostgresDatabasePreparation(ctx, lease, source, exports, oid)
		if err != nil {
			return zero, err
		}
		target, err := prepared.receipt.TargetForWorker()
		if err != nil {
			return zero, err
		}
		archive, err := store.ProjectEnvironmentClonePostgresArchiveForLease(ctx, lease, id, oid)
		if err != nil {
			return zero, err
		}
		// The archive driver is not needed to close original SQL. Recover its
		// original identity rather than trusting a replacement worker's config.
		artifact := clonePostgresArchiveStorage{ID: archive.StorageID, Fingerprint: archive.StorageFingerprint}
		if _, err := s.projectEnvironmentClonePostgresImportWork(ctx, lease, source, exports, oid, target, artifact, "", "", 0, true); err != nil {
			return zero, err
		}
		return verify()
	}
	archive, err := store.ProjectEnvironmentClonePostgresArchiveForLease(ctx, lease, id, oid)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return zero, err
	}
	if errors.Is(err, state.ErrNotFound) || archive.State != "retained" {
		if _, err := s.projectEnvironmentClonePostgresArchiveFromReader(ctx, lease, source, exports, oid, cfg.Artifact, cfg.ArchiveBytes, cfg.ArchiveLimits, cfg.PGDump, cfg.MaxPlainBytes); err != nil {
			return zero, err
		}
		return clonePostgresDataWorkProgress{Phase: "archive_retained"}, nil
	}
	contents, err := store.ProjectEnvironmentClonePostgresContentsForLease(ctx, lease, id, oid)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return zero, err
	}
	if errors.Is(err, state.ErrNotFound) || contents.State != "captured" {
		if _, err := s.projectEnvironmentClonePostgresContentsFromReader(ctx, lease, source, exports, oid, cfg.ContentsBytes, cfg.ContentsLimits, cfg.Read); err != nil {
			return zero, err
		}
		return clonePostgresDataWorkProgress{Phase: "contents_captured"}, nil
	}
	_, err = store.ProjectEnvironmentClonePostgresDatabaseSQLPinsForLease(ctx, lease, id, oid)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return zero, err
	}
	if errors.Is(err, state.ErrNotFound) {
		if _, _, err := s.projectEnvironmentClonePostgresDatabaseSQLPins(ctx, lease, source, exports, oid); err != nil {
			return zero, err
		}
		return clonePostgresDataWorkProgress{Phase: "database_prepared"}, nil
	}
	prepared, err := s.openProjectEnvironmentClonePostgresDatabasePreparation(ctx, lease, source, exports, oid)
	if err != nil {
		return zero, err
	}
	target, err := prepared.receipt.TargetForWorker()
	if err != nil {
		return zero, err
	}
	if _, err := s.projectEnvironmentClonePostgresImport(ctx, lease, source, exports, oid, target, cfg.Artifact, cfg.PGRestore, cfg.ScratchRoot, cfg.MaxPlainBytes); err != nil {
		return zero, err
	}
	return clonePostgresDataWorkProgress{Phase: "import_executed"}, nil
}
