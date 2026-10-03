//go:build !no_pg

package state

// adr: 435

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestPgArtifactScanBaseBinding(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	artifactScanBaseBinding(t, s)
}

func TestPgArtifactScanLifecycle(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	artifactScanLifecycle(t, s)
}
func TestPgArtifactScanRefusals(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	artifactScanRefusals(t, s)
}
func TestPgArtifactScanCurrentInputs(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	artifactScanCurrentInputs(t, s)
}

func TestPgArtifactScanAtomicAndImmutable(t *testing.T) {
	s, pool := registryVerificationPGStore(t)
	in, _, _, _ := artifactScanFixture(t, s, false)
	if _, err := pool.Exec(t.Context(), `CREATE FUNCTION reject_scan_selection() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected selection failure'; END; $$; CREATE TRIGGER reject_scan_selection BEFORE INSERT ON deployment_artifact_scan_current FOR EACH ROW EXECUTE FUNCTION reject_scan_selection()`); err != nil {
		t.Fatal(err)
	}
	before, err := s.DeploymentByID(t.Context(), in.DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); err == nil {
		t.Fatal("injected selection failure accepted")
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM deployment_artifact_scans`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial evidence: %d %v", count, err)
	}
	after, err := s.DeploymentByID(t.Context(), in.DeploymentID)
	if err != nil || string(before.ScanResult) != string(after.ScanResult) || before.ScanStatus != after.ScanStatus || !before.ScannedAt.Equal(after.ScannedAt) {
		t.Fatalf("partial main report: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `DROP TRIGGER reject_scan_selection ON deployment_artifact_scan_current; DROP FUNCTION reject_scan_selection()`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`UPDATE deployment_artifact_scans SET expires_at=expires_at WHERE id=$1`, `DELETE FROM deployment_artifact_scans WHERE id=$1`, `UPDATE deployment_artifact_scan_current SET scan_id=scan_id WHERE scan_id=$1`, `DELETE FROM deployment_artifact_scan_current WHERE scan_id=$1`} {
		_, err := pool.Exec(t.Context(), query, in.ID)
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.ConstraintName != "deployment_artifact_scan_immutable" {
			t.Fatalf("raw evidence mutation accepted: %v", err)
		}
	}
}

func TestPgArtifactScanDoesNotWaitForInputs(t *testing.T) {
	s, pool := registryVerificationPGStore(t)
	in, _, _, _ := artifactScanFixture(t, s, false)
	for _, key := range []string{"deployment", "publisher", "artifact"} {
		t.Run(key, func(t *testing.T) {
			tx, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(t.Context())
			query, id := `SELECT 1 FROM deployments WHERE id=$1 FOR UPDATE`, in.DeploymentID
			if key == "publisher" {
				query, id = `SELECT pg_advisory_xact_lock_shared(hashtextextended('gregale.application-standard.controls.' || $1::uuid::text,0))`, in.AppID
			}
			if key == "artifact" {
				query = `SELECT pg_advisory_xact_lock_shared(hashtextextended('gregale.application-standard.artifact-children.' || $1::uuid::text,0))`
			}
			if _, err := tx.Exec(t.Context(), query, id); err != nil {
				t.Fatal(err)
			}
			bounded, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			candidate := in
			candidate.ID = uuid.NewString()
			if _, err := s.PublishDeploymentArtifactScan(bounded, candidate); !errors.Is(err, ErrApplicationStandardRuntimeBusy) {
				t.Fatalf("scan waited or bypassed %s: %v", key, err)
			}
		})
	}
	if _, err := s.GetCurrentDeploymentArtifactScan(t.Context(), in.AccountID, in.AppID, in.DeploymentID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("busy scan left selected evidence: %v", err)
	}
}

func TestPgArtifactScanCustomerErasure(t *testing.T) {
	s, pool := registryVerificationPGStore(t)
	in, _, app, _ := artifactScanFixture(t, s, false)
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SoftDeleteAppCascade(t.Context(), app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetCurrentDeploymentArtifactScan(t.Context(), app.AccountID, app.ID, in.DeploymentID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted owner scan exposed: %v", err)
	}
	if _, err := s.RestoreApp(t.Context(), app.ID, api.MustLimitsFor(api.PlanPro)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ScheduleAppDeletion(t.Context(), app.ID, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimAppDeletion(t.Context(), app.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAppPermanently(t.Context(), app.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM deployment_artifact_scans)+(SELECT count(*) FROM deployment_artifact_scan_current)`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("customer erasure left scan evidence: %d %v", count, err)
	}
}
