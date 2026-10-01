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
)

func TestPgRegistryRootfsLifecycle(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	registryRootfsLifecycle(t, s)
}
func TestPgRegistryRootfsSubstitution(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	registryRootfsRejectsSubstitution(t, s)
}
func TestPgRegistryRootfsCurrentKey(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	registryRootfsCurrentKey(t, s)
}
func TestPgRegistryRootfsDeletionLifecycle(t *testing.T) {
	s, pool := registryVerificationPGStore(t)
	registryRootfsDeletionLifecycle(t, s)
	var records, pointers int
	if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM deployment_registry_rootfs),(SELECT count(*) FROM deployment_registry_rootfs_current)`).Scan(&records, &pointers); err != nil || records != 0 || pointers != 0 {
		t.Fatalf("erasure retained producer records/pointers: %d %d %v", records, pointers, err)
	}
}

func TestPgRegistryRootfsExpiredAndCancelled(t *testing.T) {
	for _, mode := range []string{"expired", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			s, pool := registryVerificationPGStore(t)
			in, parent, _, _ := registryRootfsFixture(t, s, false)
			if mode == "expired" {
				// Test-only privileged fixture creates historical expired evidence.
				// Production writes cannot mutate these timestamps.
				if _, err := pool.Exec(t.Context(), `ALTER TABLE deployment_registry_verifications DISABLE TRIGGER deployment_registry_verification_immutable`); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(t.Context(), `WITH c AS MATERIALIZED (SELECT clock_timestamp() AS now) UPDATE deployment_registry_verifications SET verified_at=c.now-interval '25 hours',expires_at=c.now-interval '1 hour' FROM c WHERE id=$1`, parent.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(t.Context(), `ALTER TABLE deployment_registry_verifications ENABLE TRIGGER deployment_registry_verification_immutable`); err != nil {
					t.Fatal(err)
				}
			} else if err := s.UpdateDeploymentStatus(t.Context(), in.DeploymentID, DeployCancelled, ""); err != nil {
				t.Fatal(err)
			}
			if _, err := s.PublishDeploymentRegistryRootfs(t.Context(), in); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
				t.Fatalf("late publication: %v", err)
			}
			assertNoRegistryRootfs(t, s, in)
		})
	}
}

func TestPgRegistryRootfsNonwaitingFences(t *testing.T) {
	s, pool := registryVerificationPGStore(t)
	in, _, _, _ := registryRootfsFixture(t, s, false)
	for _, which := range []string{"app", "deployment", "publisher", "artifact"} {
		t.Run(which, func(t *testing.T) {
			tx, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(t.Context())
			id, stmt := in.AppID, `SELECT 1 FROM apps WHERE id=$1 FOR UPDATE`
			switch which {
			case "deployment":
				id = in.DeploymentID
				stmt = `SELECT 1 FROM deployments WHERE id=$1 FOR UPDATE`
			case "publisher":
				stmt = `SELECT pg_advisory_xact_lock_shared(hashtextextended('gregale.application-standard.controls.' || $1::uuid::text,0))`
			case "artifact":
				id = in.DeploymentID
				stmt = `SELECT pg_advisory_xact_lock_shared(hashtextextended('gregale.application-standard.artifact-children.' || $1::uuid::text,0))`
			}
			if _, err := tx.Exec(t.Context(), stmt, id); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if _, err := s.PublishDeploymentRegistryRootfs(ctx, in); !errors.Is(err, ErrApplicationStandardRuntimeBusy) {
				t.Fatalf("waited/bypassed %s: %v", which, err)
			}
			if err := tx.Rollback(t.Context()); err != nil {
				t.Fatal(err)
			}
			assertNoRegistryRootfs(t, s, in)
		})
	}
	if _, err := s.PublishDeploymentRegistryRootfs(t.Context(), in); err != nil {
		t.Fatalf("retry remained blocked: %v", err)
	}
}

func TestPgRegistryRootfsImmutable(t *testing.T) {
	s, pool := registryVerificationPGStore(t)
	in, _, _, _ := registryRootfsFixture(t, s, false)
	if _, err := s.PublishDeploymentRegistryRootfs(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`UPDATE deployment_registry_rootfs SET expires_at=expires_at+interval '1 minute' WHERE id=$1`,
		`DELETE FROM deployment_registry_rootfs WHERE id=$1`,
		`INSERT INTO deployment_registry_rootfs SELECT $2,registry_verification_id,deployment_id,workload_name,input_snapshot,input_hash,published_at,expires_at FROM deployment_registry_rootfs WHERE id=$1`,
		`UPDATE deployment_registry_rootfs_current SET artifact_id=artifact_id WHERE artifact_id=$1`,
		`DELETE FROM deployment_registry_rootfs_current WHERE artifact_id=$1`,
	} {
		var err error
		if len(stmt) > 6 && stmt[:6] == "INSERT" {
			_, err = pool.Exec(t.Context(), stmt, in.ID, uuid.NewString())
		} else {
			_, err = pool.Exec(t.Context(), stmt, in.ID)
		}
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.ConstraintName != "deployment_registry_verification_immutable" {
			t.Fatalf("raw writer bypassed producer guard: %v", err)
		}
	}
}

func TestPgRegistryRootfsPublicationRollsBack(t *testing.T) {
	for _, sidecar := range []bool{false, true} {
		t.Run(map[bool]string{false: "main", true: "sidecar"}[sidecar], func(t *testing.T) {
			s, pool := registryVerificationPGStore(t)
			in, _, _, _ := registryRootfsFixture(t, s, sidecar)
			if _, err := pool.Exec(t.Context(), `CREATE FUNCTION registry_rootfs_test_reject() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test metadata write failed'; END $$`); err != nil {
				t.Fatal(err)
			}
			stmt := `CREATE TRIGGER registry_rootfs_test_failure BEFORE UPDATE OF rootfs_key ON deployments FOR EACH ROW EXECUTE FUNCTION registry_rootfs_test_reject()`
			if sidecar {
				stmt = `CREATE TRIGGER registry_rootfs_test_failure BEFORE INSERT ON deployment_sidecar_layers FOR EACH ROW EXECUTE FUNCTION registry_rootfs_test_reject()`
			}
			if _, err := pool.Exec(t.Context(), stmt); err != nil {
				t.Fatal(err)
			}
			if _, err := s.PublishDeploymentRegistryRootfs(t.Context(), in); err == nil {
				t.Fatal("metadata failure accepted producer")
			}
			var records, pointers int
			if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM deployment_registry_rootfs WHERE id=$1),(SELECT count(*) FROM deployment_registry_rootfs_current WHERE artifact_id=$1)`, in.ID).Scan(&records, &pointers); err != nil || records != 0 || pointers != 0 {
				t.Fatalf("failed transaction left evidence: %d %d %v", records, pointers, err)
			}
			assertNoRegistryRootfs(t, s, in)
			if sidecar {
				layers, err := s.ListDeploymentSidecarLayers(t.Context(), in.DeploymentID)
				if err != nil || len(layers) != 0 {
					t.Fatalf("failed transaction left sidecar: %+v %v", layers, err)
				}
			}
		})
	}
}
