//go:build !no_pg

package state_test

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

func TestPgApplicationStandardConsumerRoster(t *testing.T) {
	s, _ := state.StandardOperationPGStore(t)
	state.StandardConsumerRosterLifecycle(t, s)
}

func TestPgApplicationStandardLogConsumerClosure(t *testing.T) {
	s, _ := state.StandardOperationPGStore(t)
	standardLogConsumerClosureLifecycle(t, s)
}

func TestPgApplicationStandardLogConsumerClosureRawGuardsAndNonwaiting(t *testing.T) {
	s, pool := state.StandardOperationPGStore(t)
	ctx := t.Context()
	f := state.NewStandardLocalIntentFixture(ctx, t, s)
	c := state.StandardLogInventorySession(t, s, state.StandardLogInventoryNode(t, s).ID)
	i := state.StandardLogInventoryCurrent(t, s, f, 1)
	d := state.StandardLogDeliveryDrain(t, s, f)
	state.StandardLogInventoryWrite(t, s, c, i)
	standardLogHealthWrite(t, s, c, d, state.ApplicationStandardLogHealthEvent{EventRevision: 1, Status: "unknown", Reason: "idle"})
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT node_id FROM application_standard_log_consumers WHERE node_id=$1 FOR SHARE`, c.NodeID); err != nil {
		t.Fatal(err)
	}
	budget, cancel := context.WithTimeout(ctx, time.Second)
	_, err = s.CloseApplicationStandardLogConsumer(budget, c)
	cancel()
	if !errors.Is(err, state.ErrApplicationStandardReviewBusy) {
		t.Fatalf("closure waited on a report: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE application_standard_log_consumers SET stopped_at=$2 WHERE node_id=$1`, c.NodeID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	closed, err := s.CloseApplicationStandardLogConsumer(ctx, c)
	if err != nil || closed.StoppedAt.After(time.Now()) {
		t.Fatal("caller supplied shutdown clock")
	}
	for _, query := range []string{
		`UPDATE application_standard_log_inventories SET observed_at=clock_timestamp() WHERE node_id=$1`,
		`UPDATE application_standard_log_health SET observed_at=clock_timestamp() WHERE node_id=$1`,
		`UPDATE application_standard_log_consumers SET stopped_at=NULL WHERE node_id=$1`,
	} {
		if _, err := pool.Exec(ctx, query, c.NodeID); !errors.Is(state.StandardLogInventoryPGError(err), state.ErrApplicationStandardLogConsumerFenced) {
			t.Fatalf("closed raw write accepted: %v", err)
		}
	}
}

func TestPgApplicationStandardConsumerRosterAtomicSnapshot(t *testing.T) {
	s, pool := state.StandardOperationPGStore(t)
	ctx := t.Context()
	f := state.NewStandardLocalIntentFixture(ctx, t, s)
	c := state.StandardLogInventorySession(t, s, state.StandardLogInventoryNode(t, s).ID)
	old := state.StandardRosterRead(t, s, f)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE application_standard_log_consumers SET stopped_at=clock_timestamp() WHERE node_id=$1`, c.NodeID); err != nil {
		t.Fatal(err)
	}
	budget, cancel := context.WithTimeout(ctx, time.Second)
	during, err := s.GetApplicationStandardConsumerRoster(budget, (*f.Owner()).PersonalOrg.ID, (*f.App()).ID)
	cancel()
	if err != nil || during.Fingerprint() != old.Fingerprint() || state.StandardRosterNode(t, during, c.NodeID).LoggingStoppedAt != nil {
		t.Fatalf("roster blocked or exposed uncommitted closure: %+v %v", during, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	after := state.StandardRosterRead(t, s, f)
	if after.Fingerprint() == old.Fingerprint() || state.StandardRosterNode(t, after, c.NodeID).LoggingStoppedAt == nil {
		t.Fatal("committed shutdown omitted from snapshot")
	}
}

func TestPgApplicationStandardConsumerRosterInactiveLoggingOnly(t *testing.T) {
	s, _ := state.StandardOperationPGStore(t)
	state.StandardConsumerRosterInactiveLoggingOnly(t, s)
}

func TestPgApplicationStandardConsumerRosterRoleChangeRetainsRegisteredLogger(t *testing.T) {
	s, pool := state.StandardOperationPGStore(t)
	f := state.NewStandardLocalIntentFixture(t.Context(), t, s)
	c := state.StandardLogInventorySession(t, s, state.StandardLogInventoryNode(t, s).ID)
	if _, err := pool.Exec(t.Context(), `UPDATE compute_nodes SET role='control-plane',lifecycle='retired' WHERE id=$1`, c.NodeID); err != nil {
		t.Fatal(err)
	}
	r := state.StandardRosterRead(t, s, f)
	n := state.StandardRosterNode(t, r, c.NodeID)
	if n.Role != "control-plane" || n.Lifecycle != state.NodeLifecycleRetired || !n.LoggingRequired || n.NativeRequired || n.LoggingSession == nil || *n.LoggingSession != c {
		t.Fatal("role/lifecycle label proved false logging quiescence")
	}
	closure, err := s.CloseApplicationStandardLogConsumer(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	n = state.StandardRosterNode(t, state.StandardRosterRead(t, s, f), c.NodeID)
	if n.LoggingStoppedAt == nil || !n.LoggingStoppedAt.Equal(closure.StoppedAt) {
		t.Fatal("retired control-plane logger could not acknowledge joined shutdown")
	}
}
