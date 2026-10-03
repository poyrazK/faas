//go:build !no_pg

package state

// adr: 435. Real PostgreSQL fences and signature clocks for scanner bootstrap.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgRuntimeProducerInputsLifecycle(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	runtimeProducerInputsLifecycle(t, s)
}

func TestPgRuntimeProducerInputsRejectIncomplete(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	runtimeProducerInputsRejectIncomplete(t, s)
}

func TestPgRuntimeProducerInputsReplacement(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	runtimeProducerInputsReplacement(t, s)
}

func TestPgRuntimeProducerInputsDoNotWaitForCurrentInputs(t *testing.T) {
	s, pool := registryVerificationPGStore(t)
	_, base, app, dep := artifactScanBaseFixture(t, s)
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
			if _, err := s.GetFreshDeploymentRuntimeProducerInputs(bounded, app.AccountID, app.ID, dep.ID); !errors.Is(err, ErrApplicationStandardRuntimeBusy) {
				t.Fatalf("bootstrap waited or bypassed %s fence: %v", kind, err)
			}
			if err := tx.Rollback(t.Context()); err != nil {
				t.Fatal(err)
			}
			if _, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), app.AccountID, app.ID, dep.ID); err != nil {
				t.Fatalf("bootstrap did not recover after %s fence released: %v", kind, err)
			}
		})
	}
}

func TestPgRuntimeProducerInputsRequireCurrentSignatureAndPreserveExpiredOrigin(t *testing.T) {
	s, pool := registryVerificationPGStore(t)
	_, _, app, dep := artifactScanBaseFixture(t, s)
	root, err := s.GetCurrentDeploymentRegistryRootfs(t.Context(), app.AccountID, app.ID, dep.ID, "")
	if err != nil {
		t.Fatal(err)
	}
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
	// A test-only private insertion uses a short real storage-clock lifetime.
	// No immutable proof, producer or prior clock is updated to create expiry.
	row, err := q.InsertDeploymentRegistryVerification(t.Context(), tx, sqlc.InsertDeploymentRegistryVerificationParams{
		ID: mustPgUUID(short.ID), AccountID: mustPgUUID(app.AccountID), AppID: mustPgUUID(app.ID), DeploymentID: mustPgUUID(dep.ID),
		InputSnapshot: raw, InputHash: hash, Payload: short.Proof.Evidence.Payload, Signature: short.Proof.Evidence.Signature, TtlSeconds: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	replacement := root.Input
	replacement.ID, replacement.RegistryVerificationID, replacement.RegistryInputHash = uuid.NewString(), short.ID, hash
	root, err = s.PublishDeploymentRegistryRootfs(t.Context(), replacement)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil || !before.ExpiresAt.Equal(row.ExpiresAt.Time) {
		t.Fatalf("bootstrap did not cap at current signature clock: %v", err)
	}
	time.Sleep(time.Until(row.ExpiresAt.Time) + 30*time.Millisecond)
	if _, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), app.AccountID, app.ID, dep.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("expired latest signature fell back to an older proof: %v", err)
	}
	fresh := short
	fresh.ID = uuid.NewString()
	proof, err := s.RecordDeploymentRegistryVerification(t.Context(), fresh)
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil || after.InputHash != before.InputHash || !after.ExpiresAt.Equal(proof.ExpiresAt) {
		t.Fatalf("fresh signature could not bootstrap the same expired conversion: %v", err)
	}
	history, err := s.GetCurrentDeploymentRegistryRootfs(t.Context(), app.AccountID, app.ID, dep.ID, "")
	if err != nil || history.ID != root.ID || !history.ExpiresAt.Equal(root.ExpiresAt) {
		t.Fatalf("bootstrap rewrote immutable conversion history: %v", err)
	}
}
