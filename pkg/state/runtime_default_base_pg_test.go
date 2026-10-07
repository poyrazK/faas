//go:build !no_pg

package state

// adr: 435. Real database fences and raw native authority, simulated reports.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func runtimeDefaultPGStore(t *testing.T) nativeArtifactTestStore {
	s, _ := runtimeCapturePGStore(t)
	return s
}

func TestPgRuntimeDefaultBaseDoesNotWaitForBasePublication(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	root, base, app, dep := runtimeDefaultBaseFixture(t, s)
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
	if _, err := s.GetFreshDeploymentRuntimeProducerInputs(bounded, app.AccountID, app.ID, dep.ID); !errors.Is(err, ErrApplicationStandardRuntimeBusy) {
		t.Fatal("full-rootfs waited for or bypassed base publication", err)
	}
	raw, err := json.Marshal(root.Input)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(bounded, `SELECT application_standard_runtime_default_base($1::jsonb,$2::text)`, raw, app.Runtime)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
		t.Fatal("raw default-base authority waited or bypassed busy input", err)
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), app.AccountID, app.ID, dep.ID); err != nil {
		t.Fatal("full-rootfs did not recover after publication fence released", err)
	}
}
func TestPgRuntimeDefaultBaseLifecycle(t *testing.T) {
	runtimeDefaultBaseLifecycle(t, runtimeDefaultPGStore(t))
}
func TestPgRuntimeDefaultBaseRefusals(t *testing.T) {
	runtimeDefaultBaseRefusals(t, runtimeDefaultPGStore)
}
func TestPgRuntimeDefaultBaseNativeAuthority(t *testing.T) {
	runtimeDefaultBaseNativeAuthority(t, runtimeDefaultPGStore(t))
}

func TestPgRuntimeDefaultBaseRawAuthorityUsesPersistedRuntime(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	root, _, app, dep := runtimeDefaultBaseFixture(t, s)
	app = manageNativeArtifactApp(t, s, app)
	value := publishCleanRuntimeDefaultScan(t, s, app, dep)
	ins, _ := nativeArtifactAttempt(t, s, app, dep)
	capture, err := s.GetInstanceApplicationStandardAdmission(t.Context(), ins.ID)
	if err != nil {
		t.Fatal(err)
	}
	var deadline time.Time
	if err := pool.QueryRow(t.Context(), `SELECT application_standard_native_artifact_deadline($1::jsonb,clock_timestamp())`, capture.inputs).Scan(&deadline); err != nil || !deadline.Equal(value.ExpiresAt) {
		t.Fatal("raw full-rootfs authority omitted composed lease", err)
	}
	raw, err := json.Marshal(root.Input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `SELECT application_standard_runtime_default_base($1::jsonb,$2::text)`, raw, "node22"); err == nil {
		t.Fatal("raw default-base check accepted another runtime")
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	// Private fault injection changes the persisted boot runtime, not the capture.
	if _, err := tx.Exec(t.Context(), `UPDATE apps SET runtime='node22' WHERE id=$1::uuid`, app.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(t.Context(), `SELECT application_standard_native_artifact_deadline($1::jsonb,clock_timestamp())`, capture.inputs).Scan(&deadline); err == nil {
		t.Fatal("raw native authority trusted stale caller runtime")
	}
}
