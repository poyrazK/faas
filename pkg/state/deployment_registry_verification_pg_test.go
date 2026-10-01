//go:build !no_pg

package state

// adr: 387

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func registryVerificationPGStore(t *testing.T) (*PgStore, *pgxpool.Pool) {
	t.Helper()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	return NewPgStore(pool), pool
}
func TestPgRegistryVerificationLifecycle(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	registryVerificationLifecycle(t, s)
}
func TestPgRegistryVerificationSubstitution(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	registryVerificationRejectsSubstitution(t, s)
}
func TestPgRegistryVerificationCurrentKey(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	registryVerificationCurrentKey(t, s)
}

func TestPgRegistryVerificationNonwaitingFences(t *testing.T) {
	s, pool := registryVerificationPGStore(t)
	in, _, _ := registryVerificationFixture(t, s, false)
	for _, which := range []string{"app", "deployment", "publisher", "artifact"} {
		t.Run(which, func(t *testing.T) {
			tx, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(t.Context())
			var lock string
			id := in.AppID
			switch which {
			case "app":
				lock = `SELECT 1 FROM apps WHERE id=$1 FOR UPDATE`
			case "deployment":
				lock = `SELECT 1 FROM deployments WHERE id=$1 FOR UPDATE`
				id = in.DeploymentID
			case "publisher":
				lock = `SELECT pg_advisory_xact_lock_shared(hashtextextended('gregale.application-standard.controls.' || $1::uuid::text,0))`
			case "artifact":
				lock = `SELECT pg_advisory_xact_lock_shared(hashtextextended('gregale.application-standard.artifact-children.' || $1::uuid::text,0))`
				id = in.DeploymentID
			}
			if _, err := tx.Exec(t.Context(), lock, id); err != nil {
				t.Fatal(err)
			}
			bounded, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if _, err := s.RecordDeploymentRegistryVerification(bounded, in); !errors.Is(err, ErrApplicationStandardRuntimeBusy) {
				t.Fatalf("waited or bypassed %s: %v", which, err)
			}
			if err := tx.Rollback(t.Context()); err != nil {
				t.Fatal(err)
			}
			if _, err := s.GetLatestDeploymentRegistryVerification(t.Context(), in.AccountID, in.AppID, in.DeploymentID, ""); !errors.Is(err, ErrNotFound) {
				t.Fatalf("busy attempt left record: %v", err)
			}
		})
	}
	if _, err := s.RecordDeploymentRegistryVerification(t.Context(), in); err != nil {
		t.Fatalf("retry stayed blocked: %v", err)
	}
}

func TestPgRegistryVerificationImmutableAndErasure(t *testing.T) {
	s, pool := registryVerificationPGStore(t)
	in, app, _ := registryVerificationFixture(t, s, false)
	if _, err := s.RecordDeploymentRegistryVerification(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`UPDATE deployment_registry_verifications SET expires_at=expires_at+interval '1 hour' WHERE id=$1`,
		`UPDATE deployment_registry_verifications SET payload='forged'::bytea WHERE id=$1`,
		`DELETE FROM deployment_registry_verifications WHERE id=$1`,
		`INSERT INTO deployment_registry_verifications SELECT $2,deployment_id,app_id,account_id,workload_name,input_snapshot,input_hash,payload,signature,verified_at,expires_at FROM deployment_registry_verifications WHERE id=$1`,
	} {
		var err error
		if len(stmt) > 100 && stmt[:6] == "INSERT" {
			_, err = pool.Exec(t.Context(), stmt, in.ID, uuid.NewString())
		} else {
			_, err = pool.Exec(t.Context(), stmt, in.ID)
		}
		var p *pgconn.PgError
		if !errors.As(err, &p) || p.ConstraintName != "deployment_registry_verification_immutable" {
			t.Fatalf("raw evidence writer bypassed guard: %v", err)
		}
	}
	if _, err := s.ScheduleAppDeletion(t.Context(), app.ID, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimAppDeletion(t.Context(), app.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAppPermanently(t.Context(), app.ID); err != nil {
		t.Fatalf("evidence blocked owner erasure: %v", err)
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM deployment_registry_verifications WHERE deployment_id=$1`, in.DeploymentID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("evidence retained after erasure: %d %v", count, err)
	}
}

func TestPgRegistryVerificationHoldsCurrentPublisherFence(t *testing.T) {
	s, pool := registryVerificationPGStore(t)
	in, app, _ := registryVerificationFixture(t, s, false)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err := sqlc.New().LockDeploymentRegistryVerification(t.Context(), tx, sqlc.LockDeploymentRegistryVerificationParams{
		AppID: mustPgUUID(in.AppID), DeploymentID: mustPgUUID(in.DeploymentID), AccountID: mustPgUUID(in.AccountID), WorkloadName: "", Publisher: in.Proof.PublisherName,
	}); err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if err := s.DeleteAppTrustedSigner(bounded, app.AccountID, app.ID, "company"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("publisher changed during verification: %v", err)
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	// pgx cancellation may return before PostgreSQL releases the implicit
	// writer transaction's locks. Admission must stay busy during that window;
	// retry only busy, and prove the cancelled delete did not remove the key.
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, err := s.RecordDeploymentRegistryVerification(t.Context(), in)
		if err == nil {
			break
		}
		if !errors.Is(err, ErrApplicationStandardRuntimeBusy) || time.Now().After(deadline) {
			t.Fatalf("cancelled publisher delete changed authority or kept its fence: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPgRegistryVerificationDeletionLifecycle(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	registryVerificationDeletionLifecycle(t, s)
}
