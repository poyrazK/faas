//go:build !no_pg

// adr: 1522
// adr: 1530
package state_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRecoveryMigrationReplayPreservesCurrentWork(t *testing.T) {
	store, pool := operatorNodePGStore(t)
	node, running := seedNodeIntent(t, store, state.OperatorIntentKindNodeDrain, state.NodeLifecycleActive)
	intents := []state.OperatorIntent{
		running,
		claimNodeIntent(t, store, state.OperatorIntentKindForceRestart, uuid.NewString()),
		claimNodeIntent(t, store, state.OperatorIntentKindForcePark, uuid.NewString()),
	}
	id, err := store.InsertOperatorIntent(t.Context(), state.OperatorIntentKindNodeRetire, node.ID, nil,
		"22222222-2222-2222-2222-222222222222", "pending maintenance", json.RawMessage(`{}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := store.GetOperatorIntent(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	intents = append(intents, pending)

	// Replaying an older incoming migration must preserve the newer physical
	// source guards and capacity projection, as well as active operator claims.
	fingerprint := func(except string) string {
		t.Helper()
		var value string
		err := pool.QueryRow(t.Context(), `SELECT md5(coalesce(string_agg(pg_get_functiondef(oid), E'\n' ORDER BY oid),''))
FROM pg_proc WHERE pronamespace=current_schema()::regnamespace AND prokind='f' AND proname<>$1`, except).Scan(&value)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	wantFunctions := fingerprint("")
	// 20261018003100003_org_resource_lifecycle later rewrote the capacity
	// projection to read app_resource_accounts. Issued migrations are
	// append-only, so replaying 004 itself restores its own projection: check
	// everything else, then put the current projection back so the older 003
	// replay below is still checked against it.
	const laterProjection = "service_capacity_snapshot"
	wantOtherFunctions := fingerprint(laterProjection)
	var currentProjection string
	if err := pool.QueryRow(t.Context(), `SELECT pg_get_functiondef(to_regprocedure('service_capacity_snapshot()'))`).Scan(&currentProjection); err != nil {
		t.Fatal(err)
	}
	_, thisFile, _, _ := runtime.Caller(0)
	directory := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")
	files := []string{
		"20261017090000001_operator_restart_execution.sql",
		"20261017100000001_operator_cold_boot_recovery.sql",
		"20261017110000001_operator_recovery_submissions.sql",
		"20261017120000001_operator_park_execution.sql",
		"20261017130000001_operator_node_execution.sql",
		"20261017140000001_node_lifecycle_completion.sql",
		"20261017140000002_node_admission_fencing.sql",
		"20261017140000003_inbound_migration_reservations.sql",
		"20261017140000004_migration_source_retirement.sql",
	}
	// Exercise the full tail and an earlier migration replayed independently.
	for _, name := range append(files, files[7]) {
		source, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		up := strings.SplitN(strings.SplitN(string(source), "-- +goose Up", 2)[1], "-- +goose Down", 2)[0]
		if _, err := pool.Exec(t.Context(), up); err != nil {
			t.Fatalf("replay %s: %v", name, err)
		}
		if name == files[8] {
			if got := fingerprint(laterProjection); got != wantOtherFunctions {
				t.Fatalf("replay %s changed existing function definitions", name)
			}
			if _, err := pool.Exec(t.Context(), currentProjection); err != nil {
				t.Fatal(err)
			}
		}
		if got := fingerprint(""); got != wantFunctions {
			t.Fatalf("replay %s changed existing function definitions", name)
		}
		for _, want := range intents {
			got, err := store.GetOperatorIntent(t.Context(), want.ID)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("replay %s changed current intent: got=%+v want=%+v err=%v", name, got, want, err)
			}
		}
	}
}
