//go:build !no_pg

package state

// adr: 393

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgArtifactScanRenewal(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	artifactScanRenewal(t, s)
}
func TestPgArtifactScanRenewalCurrentKey(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	artifactScanRenewalCurrentKey(t, s)
}

func TestPgArtifactScanRenewalRejectsForeignApproval(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	artifactScanRenewalRejectsForeignApproval(t, s)
}

func TestPgArtifactScanRenewalDoesNotWaitForCurrentInputs(t *testing.T) {
	s, pool := registryVerificationPGStore(t)
	in, root, app, dep := artifactScanFixture(t, s, false)
	origin, err := s.GetDeploymentRegistryVerificationByID(t.Context(), app.AccountID, app.ID, dep.ID, root.Input.RegistryVerificationID)
	if err != nil {
		t.Fatal(err)
	}
	origin.Input.ID = uuid.NewString()
	proof, err := s.RecordDeploymentRegistryVerification(t.Context(), origin.Input)
	if err != nil {
		t.Fatal(err)
	}
	in.RegistryVerificationID, in.RegistryInputHash = proof.ID, proof.InputHash
	for _, key := range []string{"app", "deployment", "publisher", "artifact"} {
		t.Run(key, func(t *testing.T) {
			tx, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(t.Context())
			query, id := `SELECT 1 FROM deployments WHERE id=$1 FOR UPDATE`, dep.ID
			switch key {
			case "app":
				query, id = `SELECT 1 FROM apps WHERE id=$1 FOR UPDATE`, app.ID
			case "publisher":
				query, id = `SELECT pg_advisory_xact_lock_shared(hashtextextended('gregale.application-standard.controls.' || $1::uuid::text,0))`, app.ID
			case "artifact":
				query = `SELECT pg_advisory_xact_lock_shared(hashtextextended('gregale.application-standard.artifact-children.' || $1::uuid::text,0))`
			}
			if _, err := tx.Exec(t.Context(), query, id); err != nil {
				t.Fatal(err)
			}
			bounded, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if _, err := s.PublishDeploymentArtifactScan(bounded, in); !errors.Is(err, ErrApplicationStandardRuntimeBusy) {
				t.Fatalf("renewal waited or bypassed %s: %v", key, err)
			}
		})
	}
	if _, err := s.GetCurrentDeploymentArtifactScan(t.Context(), app.AccountID, app.ID, dep.ID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("busy renewal selected evidence: %v", err)
	}
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); err != nil {
		t.Fatalf("renewal remained blocked after current fences released: %v", err)
	}
}

func TestPgArtifactScanRenewsExpiredOrigin(t *testing.T) {
	s, pool := registryVerificationPGStore(t)
	in, root, app, dep := artifactScanFixture(t, s, false)
	origin, err := s.GetDeploymentRegistryVerificationByID(t.Context(), app.AccountID, app.ID, dep.ID, root.Input.RegistryVerificationID)
	if err != nil {
		t.Fatal(err)
	}
	short := origin.Input
	short.ID = uuid.NewString()
	short, hash, err := prepareRegistryVerification(short)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(short)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	q := sqlc.New()
	if err := q.AuthorizeDeploymentRegistryVerificationInsert(t.Context(), tx, mustPgUUID(short.ID)); err != nil {
		t.Fatal(err)
	}
	// Test-only private SQLC insertion gives this real storage-clock row a
	// short valid lifetime. No immutable row is updated to simulate expiry.
	row, err := q.InsertDeploymentRegistryVerification(t.Context(), tx, sqlc.InsertDeploymentRegistryVerificationParams{ID: mustPgUUID(short.ID), AccountID: mustPgUUID(app.AccountID), AppID: mustPgUUID(app.ID), DeploymentID: mustPgUUID(dep.ID), WorkloadName: "", InputSnapshot: raw, InputHash: hash, Payload: short.Proof.Evidence.Payload, Signature: short.Proof.Evidence.Signature, TtlSeconds: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	rootInput := root.Input
	rootInput.ID, rootInput.RegistryVerificationID, rootInput.RegistryInputHash = uuid.NewString(), short.ID, hash
	root, err = s.PublishDeploymentRegistryRootfs(t.Context(), rootInput)
	if err != nil {
		t.Fatal(err)
	}
	in.RootfsProducerID, in.RootfsInputHash = root.ID, root.InputHash
	time.Sleep(time.Until(row.ExpiresAt.Time) + 50*time.Millisecond)
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("expired original proof renewed legacy scan: %v", err)
	}
	fresh := short
	fresh.ID = uuid.NewString()
	approved, err := s.RecordDeploymentRegistryVerification(t.Context(), fresh)
	if err != nil {
		t.Fatal(err)
	}
	in.RegistryVerificationID, in.RegistryInputHash = approved.ID, approved.InputHash
	value, err := s.PublishDeploymentArtifactScan(t.Context(), in)
	got, readErr := s.GetCurrentDeploymentRegistryRootfs(t.Context(), app.AccountID, app.ID, dep.ID, "")
	if err != nil || readErr != nil || !value.ExpiresAt.After(root.ExpiresAt) || !got.ExpiresAt.Equal(root.ExpiresAt) || got.ID != root.ID {
		t.Fatalf("current signature did not approve same expired conversion: %v %v", err, readErr)
	}
}
