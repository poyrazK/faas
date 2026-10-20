//go:build !no_pg

package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgApplicationStandardLogInventory(t *testing.T) {
	s, _ := state.StandardOperationPGStore(t)
	state.StandardLogInventoryLifecycle(t, s)
}

func TestPgApplicationStandardLogInventoryExceptionDeadline(t *testing.T) {
	s, _ := state.StandardOperationPGStore(t)
	state.StandardLogInventoryExceptionDeadline(t, s)
}

func TestPgApplicationStandardLogInventoryNonwaitingFences(t *testing.T) {
	s, pool := state.StandardOperationPGStore(t)
	ctx := t.Context()
	f := state.NewStandardLocalIntentFixture(ctx, t, s)
	c := state.StandardLogInventorySession(t, s, state.StandardLogInventoryNode(t, s).ID)
	i := state.StandardLogInventoryCurrent(t, s, f, 1)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(hashtextextended('gregale.application-standard.controls.'||$1::uuid::text,0))`, (*f.App()).ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordApplicationStandardLogInventory(ctx, c, i); !errors.Is(err, state.ErrApplicationStandardReviewBusy) {
		t.Fatalf("inventory failed to fence phantom child writes: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE app_application_standards SET desired_revision=desired_revision+1,state='pending' WHERE app_id=$1`, (*f.App()).ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordApplicationStandardLogInventory(ctx, c, i); !errors.Is(err, state.ErrApplicationStandardReviewBusy) {
		t.Fatalf("inventory waited on intent writer: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordApplicationStandardLogInventory(ctx, c, i); !errors.Is(err, state.ErrApplicationStandardLogDeliveryStale) {
		t.Fatal("committed intent accepted old inventory")
	}
}

func TestPgApplicationStandardLogInventoryRawGuardAndClock(t *testing.T) {
	s, pool := state.StandardOperationPGStore(t)
	ctx := t.Context()
	f := state.NewStandardLocalIntentFixture(ctx, t, s)
	c := state.StandardLogInventorySession(t, s, state.StandardLogInventoryNode(t, s).ID)
	i := state.StandardLogInventoryCurrent(t, s, f, 1)
	state.StandardLogInventoryWrite(t, s, c, i)
	if _, err := pool.Exec(ctx, `UPDATE application_standard_log_inventories SET inventory=jsonb_set(inventory,'{drains}','[]') WHERE app_id=$1`, (*f.App()).ID); !errors.Is(state.StandardLogInventoryPGError(err), state.ErrApplicationStandardLogDeliveryStale) {
		t.Fatalf("raw incomplete inventory accepted: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE application_standard_log_inventories SET session_id=$2 WHERE app_id=$1`, (*f.App()).ID, uuid.NewString()); !errors.Is(state.StandardLogInventoryPGError(err), state.ErrApplicationStandardLogConsumerFenced) {
		t.Fatalf("raw wrong session accepted: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE application_standard_log_consumers SET generation=generation+5 WHERE node_id=$1`, c.NodeID); !errors.Is(state.StandardLogInventoryPGError(err), state.ErrApplicationStandardLogDeliveryStale) {
		t.Fatalf("raw generation jump accepted: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE application_standard_log_inventories SET observed_at=$2 WHERE app_id=$1`, (*f.App()).ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListApplicationStandardLogInventories(ctx, i.OrgID, i.AppID)
	if err != nil || len(rows) != 1 || rows[0].ObservedAt.After(time.Now()) {
		t.Fatal("caller supplied observation clock")
	}
	standardLogInventoryAgePGFixture(t, s, (*f.App()).ID)
	if _, err := s.ScheduleAppDeletion(ctx, (*f.App()).ID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimAppDeletion(ctx, (*f.App()).ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAppPermanently(ctx, (*f.App()).ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM application_standard_log_inventories WHERE app_id=$1`, (*f.App()).ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("erased app retained private inventory")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM compute_nodes WHERE id=$1`, c.NodeID); err != nil {
		t.Fatalf("node cascade blocked retained session erasure: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM application_standard_log_consumer_sessions WHERE node_id=$1`, c.NodeID).Scan(&count); err != nil || count != 0 {
		t.Fatal("erased node retained session history")
	}
}

func standardLogInventoryAgePGFixture(t *testing.T, s *state.PgStore, appID string) {
	t.Helper()
	// Age an isolated test-database fact; production guards own all timestamps.
	tx, err := (*s.Pool()).Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err := tx.Exec(t.Context(), `ALTER TABLE application_standard_log_inventories DISABLE TRIGGER application_standard_log_inventory_current`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `UPDATE application_standard_log_inventories SET observed_at=clock_timestamp()-make_interval(secs=>$2) WHERE app_id=$1`, appID, api.ApplicationStandardLogInventoryFreshness.Seconds()+1); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `ALTER TABLE application_standard_log_inventories ENABLE TRIGGER application_standard_log_inventory_current`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	var orgID string
	if err := (*s.Pool()).QueryRow(t.Context(), `SELECT org_id::text FROM apps WHERE id=$1`, appID).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListApplicationStandardLogInventories(t.Context(), orgID, appID)
	if err != nil || len(rows) != 0 {
		t.Fatal("offline node supplied fresh observation")
	}
}
