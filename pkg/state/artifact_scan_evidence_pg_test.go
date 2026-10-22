//go:build !no_pg

package state_test

// adr: 435

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgArtifactEvidenceLifecycle(t *testing.T) {
	s, _ := state.RegistryVerificationPGStore(t)
	artifactEvidenceLifecycle(t, s)
}
func TestPgArtifactEvidenceCannotBecomeLegacy(t *testing.T) {
	s, _ := state.RegistryVerificationPGStore(t)
	artifactEvidenceCannotBecomeLegacy(t, s)
}

func TestPgArtifactEvidenceRequiresBase(t *testing.T) {
	s, _ := state.RegistryVerificationPGStore(t)
	artifactEvidenceRequiresBase(t, s)
}

func TestPgArtifactEvidenceDatabaseAgesOut(t *testing.T) {
	s, _ := state.RegistryVerificationPGStore(t)
	artifactEvidenceDatabaseAgesOut(t, s)
}

func TestPgArtifactEvidenceDoesNotWaitForBase(t *testing.T) {
	s, pool := state.RegistryVerificationPGStore(t)
	in, base, app, dep := state.ArtifactScanBaseFixture(t, s)
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err := tx.Exec(t.Context(), `SELECT pg_advisory_xact_lock(hashtextextended('gregale.base-producer.' || $1::text,0))`, base.Input.Artifact.StorageKey); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := s.GetFreshDeploymentArtifactScan(ctx, app.AccountID, app.ID, dep.ID, ""); !errors.Is(err, state.ErrApplicationStandardRuntimeBusy) {
		t.Fatalf("fresh component waited/bypassed current base fence: %v", err)
	}
	if _, err := s.GetFreshDeploymentArtifactScanEvidence(ctx, app.AccountID, app.ID, dep.ID); !errors.Is(err, state.ErrApplicationStandardRuntimeBusy) {
		t.Fatalf("fresh complete set waited/bypassed current base fence: %v", err)
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFreshDeploymentArtifactScan(t.Context(), app.AccountID, app.ID, dep.ID, ""); err != nil {
		t.Fatalf("fresh component did not recover when base fence released: %v", err)
	}
}

func TestPgArtifactEvidenceExpiredLease(t *testing.T) {
	s, pool := state.RegistryVerificationPGStore(t)
	in, _, app, dep := state.ArtifactScanFixture(t, s, false)
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	in.ID = uuid.NewString()
	in, hash, err := state.PrepareDeploymentArtifactScan(in)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	q := sqlc.New()
	if err := q.AuthorizeDeploymentArtifactScanInsert(t.Context(), tx, state.MustPgUUID(in.ID)); err != nil {
		t.Fatal(err)
	}
	// Private test-only publication sets a shorter valid lease. No immutable
	// row is updated, and the earlier clean legacy report remains unchanged.
	row, err := q.InsertDeploymentArtifactScan(t.Context(), tx, sqlc.InsertDeploymentArtifactScanParams{ID: state.MustPgUUID(in.ID), ProducerID: state.MustPgUUID(in.RootfsProducerID), InputSnapshot: raw, InputHash: hash, TtlSeconds: 1, DbMaxAgeSeconds: api.ApplicationStandardScannerDBMaxAge.Seconds()})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.SelectDeploymentArtifactScan(t.Context(), tx, sqlc.SelectDeploymentArtifactScanParams{ID: row.ID, DeploymentID: row.DeploymentID, WorkloadName: row.WorkloadName}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFreshDeploymentArtifactScan(t.Context(), app.AccountID, app.ID, dep.ID, ""); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Until(row.ExpiresAt.Time) + 20*time.Millisecond)
	if _, err := s.GetFreshDeploymentArtifactScan(t.Context(), app.AccountID, app.ID, dep.ID, ""); !errors.Is(err, state.ErrApplicationStandardRuntimeStale) {
		t.Fatalf("expired private scan remained fresh: %v", err)
	}
	if _, err := s.GetFreshDeploymentArtifactScanEvidence(t.Context(), app.AccountID, app.ID, dep.ID); !errors.Is(err, state.ErrApplicationStandardRuntimeStale) {
		t.Fatalf("expired private evidence remained fresh: %v", err)
	}
	history, err := s.GetCurrentDeploymentArtifactScan(t.Context(), app.AccountID, app.ID, dep.ID, "")
	if err != nil || history.ID != in.ID || !history.ExpiresAt.Equal(row.ExpiresAt.Time) {
		t.Fatalf("expiry read changed immutable history: %v", err)
	}
}

func TestPgArtifactEvidenceDoesNotWaitForInputs(t *testing.T) {
	s, pool := state.RegistryVerificationPGStore(t)
	in, _, app, dep := state.ArtifactScanFixture(t, s, false)
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"app", "deployment", "publisher", "artifact"} {
		t.Run(kind, func(t *testing.T) {
			tx, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(t.Context())
			query, id := `SELECT 1 FROM apps WHERE id=$1 FOR UPDATE`, app.ID
			switch kind {
			case "deployment":
				query, id = `SELECT 1 FROM deployments WHERE id=$1 FOR UPDATE`, dep.ID
			case "publisher":
				query = `SELECT pg_advisory_xact_lock_shared(hashtextextended('gregale.application-standard.controls.' || $1::uuid::text,0))`
			case "artifact":
				query, id = `SELECT pg_advisory_xact_lock_shared(hashtextextended('gregale.application-standard.artifact-children.' || $1::uuid::text,0))`, dep.ID
			}
			if _, err := tx.Exec(t.Context(), query, id); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if _, err := s.GetFreshDeploymentArtifactScan(ctx, app.AccountID, app.ID, dep.ID, ""); !errors.Is(err, state.ErrApplicationStandardRuntimeBusy) {
				t.Fatalf("fresh scan waited/bypassed %s: %v", kind, err)
			}
			if _, err := s.GetFreshDeploymentArtifactScanEvidence(ctx, app.AccountID, app.ID, dep.ID); !errors.Is(err, state.ErrApplicationStandardRuntimeBusy) {
				t.Fatalf("fresh component set waited/bypassed %s: %v", kind, err)
			}
		})
	}
	if _, err := s.GetFreshDeploymentArtifactScanEvidence(t.Context(), app.AccountID, app.ID, dep.ID); err != nil {
		t.Fatalf("fresh read did not recover after fences released: %v", err)
	}
}
