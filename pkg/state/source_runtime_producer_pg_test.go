//go:build !no_pg

package state

// adr: 435. Real storage clocks and nonwaiting source claim fences.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgSourceRuntimeRenewal(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	sourceRuntimeRenewal(t, s)
}
func TestPgSourceRuntimeScanLifecycle(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	sourceRuntimeScanLifecycle(t, s)
}
func TestPgSourceRuntimePublisherReplacement(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	sourceRuntimePublisherReplacement(t, s)
}
func TestPgSourceRuntimeInvalidation(t *testing.T) {
	for _, mode := range []string{"base", "build", "metadata", "manifest"} {
		t.Run(mode, func(t *testing.T) { s, _ := registryVerificationPGStore(t); sourceRuntimeInvalidation(t, s, mode) })
	}
}

func TestPgSourceRuntimeNativeCaptureRefusesLegacyFallback(t *testing.T) {
	s, pool := registryVerificationPGStore(t)
	f, _, _ := sourceRuntimeFixture(t, s)
	var raw []byte
	err := pool.QueryRow(t.Context(), `SELECT application_standard_runtime_producers(a,d) FROM apps a JOIN deployments d ON d.app_id=a.id WHERE d.id=$1`, f.Dep.ID).Scan(&raw)
	if !errors.Is(registryVerificationError(err), ErrApplicationStandardRuntimeStale) {
		t.Fatal("source capture downgraded to legacy", err)
	}
}

func insertShortSourceApproval(t *testing.T, s *PgStore, in BuildExportPublicationInput) BuildExportPublication {
	t.Helper()
	in.ID = uuid.NewString()
	in, hash, err := prepareBuildExportPublication(in)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err := lockBuildExportPublication(t.Context(), tx, in, true); err != nil {
		t.Fatal(err)
	}
	q := sqlc.New()
	if err := q.AuthorizeBuildExportPublicationInsert(t.Context(), tx, mustPgUUID(in.ID)); err != nil {
		t.Fatal(err)
	}
	row, err := q.InsertBuildExportPublication(t.Context(), tx, sqlc.InsertBuildExportPublicationParams{
		ID: mustPgUUID(in.ID), BuildID: mustPgUUID(in.Claims.BuildID), DeploymentID: mustPgUUID(in.Claims.DeploymentID), AppID: mustPgUUID(in.Claims.AppID), AccountID: mustPgUUID(in.Claims.AccountID),
		InputSnapshot: raw, InputHash: hash, Payload: in.Proof.Payload, Signature: in.Proof.Signature, TtlSeconds: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	value, err := buildExportPublicationRow(row)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestPgSourceRuntimeExpiredOriginRequiresFreshExactClaim(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	f := sourceBuildRootfsFixture(t, s)
	short := insertShortSourceApproval(t, s, f.Parent.Input)
	f.Input.PublicationID, f.Input.PublicationHash = short.ID, short.InputHash
	root, err := s.PublishSourceBuildRootfs(t.Context(), f.Input)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), f.App.AccountID, f.App.ID, f.Dep.ID)
	if err != nil || !before.ExpiresAt.Equal(short.ExpiresAt) {
		t.Fatal("source approval clock uncapped", err)
	}
	time.Sleep(time.Until(short.ExpiresAt) + 30*time.Millisecond)
	if _, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), f.App.AccountID, f.App.ID, f.Dep.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatal("expired source approval fell back", err)
	}
	short.Input.ID = uuid.NewString()
	fresh, err := s.RecordBuildExportPublication(t.Context(), short.Input)
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), f.App.AccountID, f.App.ID, f.Dep.ID)
	if err != nil || after.InputHash != before.InputHash || !after.ExpiresAt.Equal(fresh.ExpiresAt) {
		t.Fatal("fresh exact claim cannot renew expired conversion", err)
	}
	history, err := s.GetCurrentSourceBuildRootfs(t.Context(), f.App.AccountID, f.App.ID, f.Dep.ID)
	if err != nil || history.ID != root.ID || !history.ExpiresAt.Equal(root.ExpiresAt) {
		t.Fatal("renewal extended immutable conversion clock", err)
	}
}

func TestPgSourceRuntimeInputsAreNonwaiting(t *testing.T) {
	s, pool := registryVerificationPGStore(t)
	f, root, _ := sourceRuntimeFixture(t, s)
	queued := uuid.NewString()
	if _, err := pool.Exec(t.Context(), `INSERT INTO builds(id,deployment_id,kind,source_bytes,status) VALUES($1,$2,'tarball',1,'queued')`, queued, f.Dep.ID); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"app", "deployment", "build", "queued build", "root", "pointer", "approval", "publisher", "artifact", "base"} {
		t.Run(target, func(t *testing.T) {
			tx, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(t.Context())
			query, id := `SELECT id FROM apps WHERE id=$1 FOR UPDATE`, f.App.ID
			switch target {
			case "deployment":
				query, id = `SELECT id FROM deployments WHERE id=$1 FOR UPDATE`, f.Dep.ID
			case "build":
				query, id = `SELECT id FROM builds WHERE id=$1 FOR UPDATE`, f.Build.ID
			case "queued build":
				query, id = `SELECT id FROM builds WHERE id=$1 FOR UPDATE`, queued
			case "root":
				query, id = `SELECT id FROM source_build_rootfs WHERE id=$1 FOR UPDATE`, root.ID
			case "pointer":
				query, id = `SELECT artifact_id FROM source_build_rootfs_current WHERE deployment_id=$1 FOR UPDATE`, f.Dep.ID
			case "approval":
				query, id = `SELECT id FROM build_export_publications WHERE id=$1 FOR UPDATE`, f.Parent.ID
			case "publisher":
				query = `SELECT pg_advisory_xact_lock_shared(hashtextextended('gregale.application-standard.controls.'||$1::text,0))`
			case "artifact":
				query, id = `SELECT pg_advisory_xact_lock_shared(hashtextextended('gregale.application-standard.artifact-children.'||$1::text,0))`, f.Dep.ID
			case "base":
				query, id = `SELECT pg_advisory_xact_lock(hashtextextended('gregale.base-producer.'||$1::text,0))`, f.Base.Input.Artifact.StorageKey
			}
			if _, err := tx.Exec(t.Context(), query, id); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if _, err := s.GetFreshDeploymentRuntimeProducerInputs(ctx, f.App.AccountID, f.App.ID, f.Dep.ID); !errors.Is(err, ErrApplicationStandardRuntimeBusy) {
				t.Fatal("source bootstrap waited or bypassed fence", target, err)
			}
			if err := tx.Rollback(t.Context()); err != nil {
				t.Fatal(err)
			}
			if _, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), f.App.AccountID, f.App.ID, f.Dep.ID); err != nil {
				t.Fatal("source bootstrap failed after unlock", target, err)
			}
		})
	}
}
