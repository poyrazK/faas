//go:build !no_pg

package state

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPgApplicationStandardConsumerRoster(t *testing.T) {
	s, _ := standardOperationPGStore(t)
	standardConsumerRosterLifecycle(t, s)
}

func TestPgApplicationStandardLogConsumerClosure(t *testing.T) {
	s, _ := standardOperationPGStore(t)
	standardLogConsumerClosureLifecycle(t, s)
}

func TestPgApplicationStandardLogConsumerClosureRawGuardsAndNonwaiting(t *testing.T) {
	s, pool := standardOperationPGStore(t)
	ctx := t.Context()
	f := newStandardLocalIntentFixture(ctx, t, s)
	c := standardLogInventorySession(t, s, standardLogInventoryNode(t, s).ID)
	i := standardLogInventoryCurrent(t, s, f, 1)
	d := standardLogDeliveryDrain(t, s, f)
	standardLogInventoryWrite(t, s, c, i)
	standardLogHealthWrite(t, s, c, d, ApplicationStandardLogHealthEvent{EventRevision: 1, Status: "unknown", Reason: "idle"})
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
	if !errors.Is(err, ErrApplicationStandardReviewBusy) {
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
		if _, err := pool.Exec(ctx, query, c.NodeID); !errors.Is(standardLogInventoryPGError(err), ErrApplicationStandardLogConsumerFenced) {
			t.Fatalf("closed raw write accepted: %v", err)
		}
	}
}

func TestPgApplicationStandardConsumerRosterAtomicSnapshot(t *testing.T) {
	s, pool := standardOperationPGStore(t)
	ctx := t.Context()
	f := newStandardLocalIntentFixture(ctx, t, s)
	c := standardLogInventorySession(t, s, standardLogInventoryNode(t, s).ID)
	old := standardRosterRead(t, s, f)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE application_standard_log_consumers SET stopped_at=clock_timestamp() WHERE node_id=$1`, c.NodeID); err != nil {
		t.Fatal(err)
	}
	budget, cancel := context.WithTimeout(ctx, time.Second)
	during, err := s.GetApplicationStandardConsumerRoster(budget, f.owner.PersonalOrg.ID, f.app.ID)
	cancel()
	if err != nil || during.Fingerprint() != old.Fingerprint() || standardRosterNode(t, during, c.NodeID).LoggingStoppedAt != nil {
		t.Fatalf("roster blocked or exposed uncommitted closure: %+v %v", during, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	after := standardRosterRead(t, s, f)
	if after.Fingerprint() == old.Fingerprint() || standardRosterNode(t, after, c.NodeID).LoggingStoppedAt == nil {
		t.Fatal("committed shutdown omitted from snapshot")
	}
}

func TestPgApplicationStandardConsumerRosterInactiveLoggingOnly(t *testing.T) {
	s, _ := standardOperationPGStore(t)
	standardConsumerRosterInactiveLoggingOnly(t, s)
}

func TestPgApplicationStandardConsumerRosterRoleChangeRetainsRegisteredLogger(t *testing.T) {
	s, pool := standardOperationPGStore(t)
	f := newStandardLocalIntentFixture(t.Context(), t, s)
	c := standardLogInventorySession(t, s, standardLogInventoryNode(t, s).ID)
	if _, err := pool.Exec(t.Context(), `UPDATE compute_nodes SET role='control-plane',lifecycle='retired' WHERE id=$1`, c.NodeID); err != nil {
		t.Fatal(err)
	}
	r := standardRosterRead(t, s, f)
	n := standardRosterNode(t, r, c.NodeID)
	if n.Role != "control-plane" || n.Lifecycle != NodeLifecycleRetired || !n.LoggingRequired || n.NativeRequired || n.LoggingSession == nil || *n.LoggingSession != c {
		t.Fatal("role/lifecycle label proved false logging quiescence")
	}
	closure, err := s.CloseApplicationStandardLogConsumer(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	n = standardRosterNode(t, standardRosterRead(t, s, f), c.NodeID)
	if n.LoggingStoppedAt == nil || !n.LoggingStoppedAt.Equal(closure.StoppedAt) {
		t.Fatal("retired control-plane logger could not acknowledge joined shutdown")
	}
}
