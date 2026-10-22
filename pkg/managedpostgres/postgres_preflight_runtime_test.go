package managedpostgres

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state"
)

func completeRuntimePreflight(t *testing.T, s *PostgresStore, c Cutover) CutoverPreflight {
	t.Helper()
	if _, err := s.RequestCutoverPreflight(t.Context(), c.AccountID, c.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	p, err := s.EvaluateCutoverPreflight(t.Context(), c.AccountID, c.ID, time.Now().UTC())
	if err != nil || p.RuntimeDrain == nil {
		t.Fatal(p, err)
	}
	return p
}

func TestPostgresCutoverPreflightRuntimeDrainRecoveryAndInvalidation(t *testing.T) {
	s, ps, c, node, instances := cutoverDrainPlanFixture(t,
		drainPlanInstanceSpec{"default", "normal", "running"},
		drainPlanInstanceSpec{"production", "job", "stopped"},
		drainPlanInstanceSpec{"staging", "worker", "parked"})
	p := completeRuntimePreflight(t, s, c)
	if p.RuntimeDrain.State != "not_fenced" {
		t.Fatal(p.RuntimeDrain)
	}
	if _, err := s.FenceCutoverAdmission(t.Context(), c.AccountID, c.ID); err != nil {
		t.Fatal(err)
	}
	p, err := s.GetCutoverPreflight(t.Context(), c.AccountID, c.ID)
	if err != nil || !p.RuntimeDrainChanged || p.RuntimeDrain.State != "not_fenced" {
		t.Fatal("fence change did not invalidate historical observation", p, err)
	}
	p = completeRuntimePreflight(t, s, c)
	if p.RuntimeDrain.State != "missing" || !slices.Contains(p.RuntimeDrain.Blockers, "drain_plan_missing") {
		t.Fatal("preflight reserved a plan", p)
	}
	plan, err := ps.ReserveManagedPostgresDrainPlan(t.Context(), c.ID, node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RequestCutoverPreflight(t.Context(), c.AccountID, c.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	// A replacement control-plane adapter recovers the pending report.
	restarted, err := NewPostgresStore(s.pool)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := restarted.PendingCutoverPreflights(t.Context(), 10)
	if err != nil || len(rows) != 1 || rows[0].ID != c.ID {
		t.Fatal(rows, err)
	}
	p, err = restarted.EvaluateCutoverPreflight(t.Context(), c.AccountID, c.ID, time.Now().UTC())
	if err != nil || p.RuntimeDrain.State != "blocked" || p.RuntimeDrain.Pending != 2 || p.RuntimeDrain.Blocked != 1 || p.RuntimeDrain.Retired != 0 || p.RuntimeDrain.InventoryCount != 3 {
		t.Fatal("terminal rows or unsupported members fabricated receipts", p, err)
	}
	for _, member := range p.RuntimeDrain.Members {
		if member.InstanceID == instances[2].ID && member.BlockerCode != "parked_instance_unproven" {
			t.Fatal("parked blocker lost", member)
		}
		if member.InstanceID == instances[1].ID && (member.Scope != "production" || member.Mode != "job" || member.State != "pending" || member.BlockerCode != "native_retirement_unconfirmed") {
			t.Fatal("other scope or stopped-without-receipt omitted", member)
		}
	}
	before, err := restarted.GetCutoverPreflight(t.Context(), c.AccountID, c.ID)
	if err != nil || before.RuntimeDrainChanged {
		t.Fatal("unchanged evidence marked stale", before, err)
	}
	snapshot, err := s.pool.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snapshot.Rollback(t.Context()) }()
	initial, err := state.ObserveManagedPostgresDrainPlan(t.Context(), snapshot, plan.Frame.ID)
	if err != nil || initial.Pending != 2 {
		t.Fatal(initial, err)
	}
	for _, member := range plan.Members {
		if member.Drain != nil {
			if err := ps.CompleteManagedPostgresDrain(t.Context(), member.Drain.Frame, cutoverDrainProof(member.Drain.Frame)); err != nil {
				t.Fatal(err)
			}
		}
	}
	consistent, err := state.ObserveManagedPostgresDrainPlan(t.Context(), snapshot, plan.Frame.ID)
	if err != nil || consistent.Pending != initial.Pending || consistent.Retired != initial.Retired {
		t.Fatal("receipt reads escaped the observation snapshot", consistent, err)
	}
	if err := snapshot.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, err := restarted.GetCutoverPreflight(t.Context(), c.AccountID, c.ID)
	if err != nil || !after.RuntimeDrainChanged || after.RuntimeDrain.Retired != 0 || !after.CheckedAt.Equal(before.CheckedAt) || after.Revision != before.Revision {
		t.Fatal("new receipts silently rewrote the report", after, err)
	}
	p = completeRuntimePreflight(t, restarted, c)
	if p.RuntimeDrain.Retired != 2 || p.RuntimeDrain.Blocked != 1 || p.RuntimeDrain.Pending != 0 || !slices.Contains(p.Blockers, "runtime_safety_unproven") || !slices.Contains(p.Blockers, "source_sessions_unfenced") {
		t.Fatal("partial retirement cleared an independent safety gate", p)
	}
	for _, account := range []string{uuid.NewString()} {
		if _, err := restarted.GetCutoverPreflight(t.Context(), account, c.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal("runtime inventory crossed account boundary", err)
		}
	}
	// The public summary must not contain native or host authority identities.
	raw, err := json.Marshal(p.RuntimeDrain)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{node.ID, plan.Frame.ID, c.Source.ProviderResourceID, "owner_revision", "native_generation", "kernel_boot_id", "fingerprint"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("private runtime authority exposed", private)
		}
	}
	// Old reports from before the migration can be read, but require a retry.
	if _, err := s.pool.Exec(t.Context(), `UPDATE managed_postgres_cutover_preflights SET runtime_drain=NULL,runtime_drain_fingerprint='' WHERE cutover_id=$1`, c.ID); err != nil {
		t.Fatal(err)
	}
	legacy, err := restarted.GetCutoverPreflight(t.Context(), c.AccountID, c.ID)
	if err != nil || !legacy.RuntimeDrainChanged || legacy.RuntimeDrain != nil {
		t.Fatal("legacy report silently accepted", legacy, err)
	}
}

func TestPostgresCutoverPreflightRuntimeCatalogRetiredStillGated(t *testing.T) {
	s, ps, c, node, instances := cutoverDrainPlanFixture(t, drainPlanInstanceSpec{"default", "normal", "running"})
	plan := fenceDrainPlan(t, s, ps, c, node.ID)
	f := plan.Members[0].Drain.Frame
	if err := ps.CompleteManagedPostgresDrain(t.Context(), f, cutoverDrainProof(f)); err != nil {
		t.Fatal(err)
	}
	p := completeRuntimePreflight(t, s, c)
	if p.RuntimeDrain.State != "catalog_retired" || p.RuntimeDrain.Retired != 1 || !slices.Contains(p.Blockers, "activation_disabled") || !slices.Contains(p.Blockers, "runtime_safety_unproven") {
		t.Fatal("catalog retirement promoted activation", p)
	}
	// Confirmed retention preserves the original receipt and its observation.
	if _, err := s.pool.Exec(t.Context(), `DELETE FROM instances WHERE id=$1`, instances[0].ID); err != nil {
		t.Fatal(err)
	}
	p, err := s.GetCutoverPreflight(t.Context(), c.AccountID, c.ID)
	if err != nil || p.RuntimeDrainChanged {
		t.Fatal("confirmed retention invalidated stable receipts", p, err)
	}
	other, err := ps.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{other.ID, node.ID} {
		if _, err := s.pool.Exec(t.Context(), `UPDATE apps SET node_id=$2 WHERE id=$1`, c.AppID, owner); err != nil {
			t.Fatal(err)
		}
	}
	p, err = s.GetCutoverPreflight(t.Context(), c.AccountID, c.ID)
	if err != nil || !p.RuntimeDrainChanged || p.RuntimeDrain.State != "catalog_retired" {
		t.Fatal("owner ABA reused a completed observation", p, err)
	}
	p = completeRuntimePreflight(t, s, c)
	if p.RuntimeDrain.State != "blocked" || !slices.Contains(p.RuntimeDrain.Blockers, "owner_revision_changed") {
		t.Fatal("current owner mismatch hidden", p)
	}
	// Retry clears runtime observations as well as credential observations.
	p, err = s.RequestCutoverPreflight(t.Context(), c.AccountID, c.ID, time.Now().UTC())
	if err != nil || p.RuntimeDrain != nil || p.RuntimeDrainFingerprint != "" {
		t.Fatal("pending retry retained old runtime evidence", p, err)
	}
	for _, values := range []string{
		`runtime_drain=NULL,runtime_drain_fingerprint=repeat('a',64)`,
		`runtime_drain='{}',runtime_drain_fingerprint=''`,
		`runtime_drain='{}',runtime_drain_fingerprint=repeat('a',64)`,
	} {
		if _, err := s.pool.Exec(t.Context(), `UPDATE managed_postgres_cutover_preflights SET `+values+` WHERE cutover_id=$1`, c.ID); err == nil {
			t.Fatal("inconsistent pending runtime evidence accepted", values)
		}
	}
	up, down := cutoverMigrationStatements(t, "20261016165124515_managed_postgres_preflight_runtime_drain.sql")
	for _, migration := range []string{down, up} {
		if _, err := s.pool.Exec(t.Context(), migration); err != nil {
			t.Fatal("runtime observation migration round trip", err)
		}
	}
}
