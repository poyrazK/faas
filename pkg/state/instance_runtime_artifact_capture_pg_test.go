//go:build !no_pg

package state

// adr: 435. Real PostgreSQL enforces the producer cut below Go APIs.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestPgInstanceRuntimeArtifactCaptureRenewal(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	runtimeArtifactCaptureRenewal(t, s)
}
func TestPgInstanceRuntimeArtifactCaptureProducerReplacement(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	runtimeArtifactCaptureProducerReplacement(t, s)
}
func TestPgInstanceRuntimeArtifactCaptureTwoDrives(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	runtimeArtifactCaptureTwoDrives(t, s)
}
func TestPgInstanceRuntimeArtifactCaptureSidecars(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	runtimeArtifactCaptureSidecars(t, s)
}

func TestPgInstanceRuntimeArtifactCaptureDoesNotBackfill(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	runtimeArtifactCaptureDoesNotBackfill(t, s)
}

func TestPgInstanceRuntimeArtifactCaptureHoldsBaseFence(t *testing.T) {
	s, pool := registryVerificationPGStore(t)
	_, base, app, dep := artifactScanBaseFixture(t, s)
	node, err := s.UpsertComputeNode(t.Context(), ComputeNode{Name: "base-cut", TargetURL: "unix:///tmp/base-cut.sock", VPCPUs: 4, VCPUBudget: 4 * api.CPUOvercommit, MemMB: 4096, MaxConcurrency: 8, AdmissionCeilingMB: 4096, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	var instanceID string
	if err := tx.QueryRow(t.Context(), `INSERT INTO instances(app_id,deployment_id,state,ram_mb,node_id) VALUES($1,$2,'cold_booting',128,$3) RETURNING id::text`, app.ID, dep.ID, node.ID).Scan(&instanceID); err != nil {
		t.Fatal(err)
	}
	replacement := base.Input
	replacement.ID = uuid.NewString()
	bounded, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := s.PublishBaseImageProducer(bounded, replacement); !errors.Is(err, ErrApplicationStandardReviewBusy) {
		t.Fatalf("base publication committed inside a captured input cut: %v", err)
	}
	current, err := s.GetCurrentBaseImageProducer(t.Context(), base.Input.Artifact.StorageKey)
	if err != nil || current.ID != base.ID {
		t.Fatalf("refused base publication changed selected producer: %v", err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishBaseImageProducer(t.Context(), replacement); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishInstanceRuntime(t.Context(), instanceID, string(StateColdBooting), "stale-cut", "10.100.0.8", 20008); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("committed later base publication escaped capture comparison: %v", err)
	}
}

func TestPgInstanceRuntimeArtifactCaptureRawProducerGuard(t *testing.T) {
	s, pool := registryVerificationPGStore(t)
	_, root, app, dep := artifactScanFixture(t, s, false)
	ins, _ := createRuntimeArtifactCapture(t, s, app, dep)
	replacement := root.Input
	replacement.ID = uuid.NewString()
	if _, err := s.PublishDeploymentRegistryRootfs(t.Context(), replacement); err != nil {
		t.Fatal(err)
	}
	_, err := pool.Exec(t.Context(), `UPDATE instances SET state='running',netns='raw-stale',host_ip='10.100.0.8',guest_uid=20008 WHERE id=$1`, ins.ID)
	var failure *pgconn.PgError
	if !errors.As(err, &failure) || failure.ConstraintName != "application_standard_runtime_stale" {
		t.Fatalf("raw SQL bypassed private producer identity: %v", err)
	}
	got, err := s.InstanceByID(t.Context(), ins.ID)
	if err != nil || got.State != string(StateColdBooting) || got.Netns != "" {
		t.Fatalf("failed publication saved a runtime tuple: %v", err)
	}
}

func TestPgInstanceRuntimeArtifactCaptureBaseFence(t *testing.T) {
	s, pool := registryVerificationPGStore(t)
	_, base, app, dep := artifactScanBaseFixture(t, s)
	node, err := s.UpsertComputeNode(t.Context(), ComputeNode{Name: "base-fence", TargetURL: "unix:///tmp/base-fence.sock", VPCPUs: 4, VCPUBudget: 4 * api.CPUOvercommit, MemMB: 4096, MaxConcurrency: 8, AdmissionCeilingMB: 4096, Active: true})
	if err != nil {
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
	bounded, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := s.CreateInstance(bounded, app.ID, dep.ID, string(StateColdBooting), 128, node.ID, uuid.NewString()); !errors.Is(err, ErrApplicationStandardRuntimeBusy) {
		t.Fatalf("capture ignored or waited on current base publication: %v", err)
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM instances WHERE app_id=$1`, app.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed producer cut reserved residency: count=%d err=%v", count, err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	createRuntimeArtifactCapture(t, s, app, dep)
}
