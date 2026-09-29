package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

type projectEnvironmentDatabaseCopy struct {
	source      managedpostgres.Database
	name        string
	pointInTime time.Time
}

func projectEnvironmentDatabaseCloneName(project state.Project, target, sourceDatabaseID string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{project.ID, target, sourceDatabaseID}, "\x00")))
	return "env-" + target + "-" + hex.EncodeToString(sum[:6])
}

// Retained for cleanup of copies created before database-level deduplication.
func legacyProjectEnvironmentDatabaseCloneName(project state.Project, target string, app state.App, sourceDatabaseID string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{project.ID, target, app.ID, sourceDatabaseID}, "\x00")))
	return "env-" + target + "-" + hex.EncodeToString(sum[:6])
}

// One physical copy per source database preserves relationships between apps.
// Every database copy in this preparation uses the same recorded recovery time.
// This alone does not establish a cross-provider application write barrier.
func (s *server) planProjectEnvironmentDatabaseCopies(ctx context.Context, acct state.Account, project state.Project, target string, bindings []projectEnvironmentBindingClone) (map[string]projectEnvironmentDatabaseCopy, error) {
	copies := make(map[string]projectEnvironmentDatabaseCopy)
	for _, binding := range bindings {
		if binding.kind != "managed_postgres" {
			continue
		}
		source := binding.database
		if source.ID == "" || source.AccountID != acct.ID || source.State != managedpostgres.StateReady || source.ProviderResourceID == "" || binding.postgres.DatabaseID != source.ID {
			return nil, managedpostgres.ErrConflict
		}
		if existing, ok := copies[source.ID]; ok && (existing.source.Spec != source.Spec || existing.source.ProviderResourceID != source.ProviderResourceID ||
			existing.source.BackendID != source.BackendID || existing.source.BackendFingerprint != source.BackendFingerprint) {
			return nil, managedpostgres.ErrConflict
		}
		copies[source.ID] = projectEnvironmentDatabaseCopy{source: source, name: projectEnvironmentDatabaseCloneName(project, target, source.ID)}
	}
	if len(copies) == 0 {
		return copies, nil
	}
	if s.managedPostgres == nil {
		return nil, managedpostgres.ErrUnavailable
	}
	databases, err := s.managedPostgres.List(ctx, acct.ID)
	if err != nil {
		return nil, err
	}
	if err := adoptLegacyProjectEnvironmentDatabaseCopies(copies, databases, bindings, project, target); err != nil {
		return nil, err
	}
	point, err := projectEnvironmentDatabaseCopyPoint(copies, databases)
	if err != nil {
		return nil, err
	}
	for id, copy := range copies {
		copy.pointInTime = point
		copies[id] = copy
	}
	return copies, nil
}

// A failed preparation from an older version may have persisted a restore
// before its binding. Adopt one matching copy, but never silently pick between
// multiple copies that would split the original shared database topology.
func adoptLegacyProjectEnvironmentDatabaseCopies(copies map[string]projectEnvironmentDatabaseCopy, databases []managedpostgres.Database, bindings []projectEnvironmentBindingClone, project state.Project, target string) error {
	names := make(map[string]bool, len(databases))
	for _, database := range databases {
		names[database.Name] = true
	}
	selected := make(map[string]string, len(copies))
	for id, copy := range copies {
		if names[copy.name] {
			selected[id] = copy.name
		}
	}
	for _, binding := range bindings {
		if binding.kind != "managed_postgres" {
			continue
		}
		id := binding.database.ID
		name := legacyProjectEnvironmentDatabaseCloneName(project, target, binding.app, id)
		if !names[name] {
			continue
		}
		if chosen := selected[id]; chosen != "" && chosen != name {
			return fmt.Errorf("existing database copies split the source topology: %w", managedpostgres.ErrConflict)
		}
		selected[id] = name
		copy := copies[id]
		copy.name = name
		copies[id] = copy
	}
	return nil
}

func projectEnvironmentDatabaseCopyPoint(copies map[string]projectEnvironmentDatabaseCopy, databases []managedpostgres.Database) (time.Time, error) {
	var point time.Time
	for _, database := range databases {
		for _, copy := range copies {
			if database.Name != copy.name {
				continue
			}
			if database.RestoreSourceDatabaseID != copy.source.ID || database.RestoreSourceResourceID != copy.source.ProviderResourceID || database.Spec != copy.source.Spec ||
				database.BackendID != copy.source.BackendID || database.BackendFingerprint != copy.source.BackendFingerprint || database.RestorePointInTime.IsZero() ||
				database.State == managedpostgres.StateDeleting || database.State == managedpostgres.StateDeleted {
				return time.Time{}, managedpostgres.ErrConflict
			}
			if !point.IsZero() && !point.Equal(database.RestorePointInTime) {
				return time.Time{}, fmt.Errorf("database copies have different recovery times: %w", managedpostgres.ErrConflict)
			}
			point = database.RestorePointInTime
		}
	}
	if point.IsZero() {
		// PostgreSQL timestamps retain microseconds; use the same precision for
		// reservation identity checks before and after persistence.
		point = time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond)
	}
	return point, nil
}

func (s *server) ensureProjectEnvironmentDatabaseClone(ctx context.Context, acct state.Account, copy projectEnvironmentDatabaseCopy, cleanup *[]func(context.Context) error) (managedpostgres.Database, error) {
	cloned, created, err := s.managedPostgres.RestoreWithResult(ctx, managedpostgres.RestoreDatabaseRequest{
		AccountID: acct.ID, SourceDatabaseID: copy.source.ID, Name: copy.name, PointInTime: copy.pointInTime,
	})
	if err != nil {
		// Restore persists its intent before provider I/O. Keep that row so a
		// retry resumes the recorded recovery time and provider identity.
		return managedpostgres.Database{}, err
	}
	if created {
		cloneID := cloned.ID
		*cleanup = append(*cleanup, func(cleanupCtx context.Context) error {
			_, deleteErr := s.managedPostgres.Delete(cleanupCtx, acct.ID, cloneID)
			return deleteErr
		})
	}
	if cloned.State != managedpostgres.StateReady || cloned.ID == copy.source.ID || cloned.ProviderResourceID == copy.source.ProviderResourceID ||
		cloned.RestoreSourceDatabaseID != copy.source.ID || cloned.RestoreSourceResourceID != copy.source.ProviderResourceID || cloned.Spec != copy.source.Spec ||
		cloned.BackendID != copy.source.BackendID || cloned.BackendFingerprint != copy.source.BackendFingerprint ||
		!cloned.RestorePointInTime.Equal(copy.pointInTime) {
		return managedpostgres.Database{}, fmt.Errorf("isolated PostgreSQL copy is not ready: %w", managedpostgres.ErrConflict)
	}
	return cloned, nil
}
