//go:build !no_pg

package state

// adr: 435. Actual PostgreSQL publication, immutability and nonwaiting fences.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestPgRuntimeScanLifecycle(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	runtimeScanLifecycle(t, s)
}

func TestPgRuntimeScanReplacement(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	runtimeScanReplacement(t, s)
}

func TestPgRuntimeScanPublicationAndFreshReadDoNotWait(t *testing.T) {
	s, pool := registryVerificationPGStore(t)
	_, base, app, dep := artifactScanBaseFixture(t, s)
	inputs, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	in := runtimeScanInputFixture(t, inputs)
	if _, err := s.PublishDeploymentRuntimeScan(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"app", "deployment", "publisher", "artifact", "base"} {
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
			case "base":
				query, id = `SELECT pg_advisory_xact_lock(hashtextextended('gregale.base-producer.' || $1::text,0))`, base.Input.Artifact.StorageKey
			}
			if _, err := tx.Exec(t.Context(), query, id); err != nil {
				t.Fatal(err)
			}
			bounded, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if _, err := s.PublishDeploymentRuntimeScan(bounded, in); !errors.Is(err, ErrApplicationStandardRuntimeBusy) {
				t.Fatalf("publication bypassed/waited for %s fence: %v", kind, err)
			}
			if _, err := s.GetFreshDeploymentRuntimeScan(bounded, app.AccountID, app.ID, dep.ID); !errors.Is(err, ErrApplicationStandardRuntimeBusy) {
				t.Fatalf("fresh read bypassed/waited for %s fence: %v", kind, err)
			}
			if err := tx.Rollback(t.Context()); err != nil {
				t.Fatal(err)
			}
			if _, err := s.GetFreshDeploymentRuntimeScan(t.Context(), app.AccountID, app.ID, dep.ID); err != nil {
				t.Fatal("released fence did not recover", err)
			}
		})
	}
}

func TestPgRuntimeScanStorageGuardsRejectMutationAndReselection(t *testing.T) {
	s, pool := registryVerificationPGStore(t)
	_, _, app, dep := artifactScanBaseFixture(t, s)
	inputs, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	in := runtimeScanInputFixture(t, inputs)
	if _, err := s.PublishDeploymentRuntimeScan(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`UPDATE deployment_runtime_scans SET expires_at=expires_at+interval '1 second' WHERE id=$1`,
		`DELETE FROM deployment_runtime_scans WHERE id=$1`,
		`UPDATE deployment_runtime_scan_current SET scan_id=$1 WHERE scan_id=$1`,
		`DELETE FROM deployment_runtime_scan_current WHERE scan_id=$1`,
		`INSERT INTO deployment_runtime_scans SELECT gen_random_uuid(),deployment_id,input_snapshot,input_hash,scanned_at,expires_at FROM deployment_runtime_scans WHERE id=$1`,
	} {
		_, err := pool.Exec(t.Context(), query, in.ID)
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.ConstraintName != "deployment_runtime_scan_immutable" {
			t.Fatalf("private runtime scan mutation bypassed guard: %v", err)
		}
	}
	if _, err := s.GetFreshDeploymentRuntimeScan(t.Context(), app.AccountID, app.ID, dep.ID); err != nil {
		t.Fatal("refused writes changed current facts", err)
	}
}

func TestPgRuntimeScanDatabaseDeadlineAndExpiredRetry(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	_, _, app, dep := artifactScanBaseFixture(t, s)
	inputs, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	in := runtimeScanInputFixture(t, inputs)
	built := time.Now().UTC().Add(-api.ApplicationStandardScannerDBMaxAge + 2*time.Second)
	in.Reports[0].Report.ScannerDBBuiltAt = built.Format(time.RFC3339Nano)
	value, err := s.PublishDeploymentRuntimeScan(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !value.ExpiresAt.Equal(built.Truncate(time.Microsecond).Add(api.ApplicationStandardScannerDBMaxAge)) {
		t.Fatal("database age did not cap durable lease", value.ExpiresAt, built)
	}
	time.Sleep(time.Until(value.ExpiresAt.Add(10 * time.Millisecond)))
	if _, err := s.GetFreshDeploymentRuntimeScan(t.Context(), app.AccountID, app.ID, dep.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatal("expired database retained fresh scan", err)
	}
	retry, err := s.PublishDeploymentRuntimeScan(t.Context(), in)
	if err != nil || !retry.ExpiresAt.Equal(value.ExpiresAt) || !retry.ScannedAt.Equal(value.ScannedAt) {
		t.Fatal("expired exact retry renewed clocks", err)
	}
	in.ID = "48b2ebdc-7330-49a1-b7fe-2aa06c912e38"
	if _, err := s.PublishDeploymentRuntimeScan(t.Context(), in); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatal("expired database accepted new publication", err)
	}
	history, err := s.GetCurrentDeploymentRuntimeScan(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil || history.ID != value.ID {
		t.Fatal("expired refusal changed history", err)
	}
}
