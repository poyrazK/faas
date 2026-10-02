//go:build !no_pg

package state

// adr: 430. Real PostgreSQL producer renewal and immutable legacy handoff.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgApplicationStandardArtifactRenewalBeforeBoot(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	standardArtifactRenewalBeforeBoot(t, s)
}
func TestPgApplicationStandardArtifactRenewalPromotion(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	standardArtifactRenewalPromotion(t, s)
}
func TestPgApplicationStandardArtifactRenewalRefusesUnsafe(t *testing.T) {
	standardArtifactRenewalRefusesUnsafe(t, func(t *testing.T) nativeArtifactTestStore {
		s, _ := runtimeCapturePGStore(t)
		return s
	})
}

func TestPgApplicationStandardArtifactRenewalLegacyCapture(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	in, _, app, dep := artifactScanFixture(t, s, false)
	app = manageNativeArtifactApp(t, s, app)
	in = publishRenewedStandardScan(t, s, in, "LOW")
	var restore string
	if err := pool.QueryRow(t.Context(), `SELECT pg_get_functiondef('application_standard_stable_runtime_input(jsonb)'::regprocedure)`).Scan(&restore); err != nil {
		t.Fatal(err)
	}
	// Only this private clone models capture creation by the pre-upgrade binary.
	// The resulting immutable admission row is never updated or rehashed.
	if _, err := pool.Exec(t.Context(), `CREATE OR REPLACE FUNCTION application_standard_stable_runtime_input(input jsonb) RETURNS jsonb LANGUAGE sql IMMUTABLE AS $$ SELECT input $$`); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(context.Background(), restore)
	ins, capture := createRuntimeArtifactCapture(t, s, app, dep)
	if !bytes.Contains(capture.inputs, []byte("scan_result_hash")) {
		t.Fatal("fixture did not create a legacy capture")
	}
	if _, err := pool.Exec(t.Context(), restore); err != nil {
		t.Fatal(err)
	}
	candidate := runtimeCaptureTestBinding(t, s, ins)
	publishRenewedStandardScan(t, s, in, "LOW")
	assertRenewalCallerAdmission(t, s, ins, app, dep)
	grant, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, candidate)
	if err != nil {
		t.Fatalf("fresh approval could not hand off legacy capture: %v", err)
	}
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateRunning, nativeArtifactReceipt(grant, false)); err != nil {
		t.Fatal(err)
	}
	assertStandardCaptureHistory(t, s, capture)
}

func TestPgApplicationStandardArtifactRenewalRawPublication(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	in, _, app, dep := artifactScanFixture(t, s, false)
	app = manageNativeArtifactApp(t, s, app)
	in = publishRenewedStandardScan(t, s, in, "LOW")
	ins, candidate := nativeArtifactAttempt(t, s, app, dep)
	grant, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, candidate)
	if err != nil {
		t.Fatal(err)
	}
	r := nativeArtifactReceipt(grant, false)
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlc.New().RecordInstanceApplicationStandardReceipt(t.Context(), pool, sqlc.RecordInstanceApplicationStandardReceiptParams{Token: mustPgUUID(grant.Token), Receipt: raw}); err != nil {
		t.Fatal(err)
	}
	publishRenewedStandardScan(t, s, in, "failed")
	query := `UPDATE instances SET state='running',netns=$2,host_ip=$3::inet,guest_uid=$4,application_standard_boot_token=$5 WHERE id=$1`
	if _, err := pool.Exec(t.Context(), query, ins.ID, r.Netns, r.HostIP, r.LeaseUID, grant.Token); !errors.Is(mapErr(err), ErrApplicationStandardRuntimeStale) {
		t.Fatalf("raw publication bypassed failed current approval: %v", err)
	}
	assertNativeBootUnpublished(t, s, ins)
	publishRenewedStandardScan(t, s, in, "LOW")
	if _, err := pool.Exec(t.Context(), query, ins.ID, r.Netns, r.HostIP, r.LeaseUID, grant.Token); err != nil {
		t.Fatalf("raw publication rejected renewed unchanged producer: %v", err)
	}
	publishRenewedStandardScan(t, s, in, "failed")
	if _, err := pool.Exec(t.Context(), `UPDATE instances SET state='migrating',lease_token=$2,migration_started_at=$3 WHERE id=$1`, ins.ID, uuid.NewString(), time.Now()); !errors.Is(mapErr(err), ErrApplicationStandardRuntimeStale) {
		t.Fatalf("raw migration bypassed failed current approval: %v", err)
	}
	current, err := s.InstanceByID(t.Context(), ins.ID)
	if err != nil || current.State != string(StateRunning) {
		t.Fatalf("raw refused migration changed residency: %v", err)
	}
}
