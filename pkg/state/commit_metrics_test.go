package state_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgCommitObservationSummaryLifecycle(t *testing.T) {
	store, pool, source, _ := commitManagedFixture(t)
	ctx := t.Context()
	read := func() state.CommitRelayObservation {
		t.Helper()
		snapshot, err := store.CommitRelayObservationSummary(ctx, time.Now().Add(-5*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	if got := read(); got.EnabledSources != 1 || got.UnknownSources != 1 {
		t.Fatalf("unconfigured=%+v", got)
	}
	if err := store.SetCommitSourceConnection(ctx, source.AccountID, source.ID, []byte("opaque-ciphertext-fixture")); err != nil {
		t.Fatal(err)
	}
	row := state.CommitRelaySource{CommitSource: source, CredentialRevision: 1}
	pending, blocked := int64(4), int64(2)
	oldest := time.Now().Add(-20 * time.Minute).UTC()
	if err := store.RecordCommitRelayStatus(ctx, row, "blocked_events", &pending, &blocked, &oldest); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.UnknownSources != 0 || got.PendingEvents != 4 || got.BlockedEvents != 2 || got.FailingSources != 0 || got.OldestPendingTimestamp <= 0 {
		t.Fatalf("known=%+v", got)
	}
	if err := store.RecordCommitRelayStatus(ctx, row, "database_unavailable", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.UnknownSources != 1 || got.PendingEvents != 0 || got.BlockedEvents != 0 || got.FailingSources != 1 || got.OldestPendingTimestamp != 0 {
		t.Fatalf("outage inferred empty/old snapshot=%+v", got)
	}
	if err := store.RecordCommitRelayStatus(ctx, row, "healthy", &pending, &blocked, &oldest); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE commit_sources SET last_checked_at=now()-interval '10 minutes' WHERE id=$1::uuid`, source.ID); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.UnknownSources != 1 || got.PendingEvents != 0 || got.OldestPendingTimestamp != 0 {
		t.Fatalf("stale=%+v", got)
	}
	if err := store.SetCommitSourceConnection(ctx, source.AccountID, source.ID, []byte("rotated-ciphertext-fixture")); err != nil {
		t.Fatal(err)
	}
	// An old pass must not make the rotated credential appear healthy.
	if err := store.RecordCommitRelayStatus(ctx, row, "healthy", &pending, &blocked, &oldest); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.UnknownSources != 1 || got.PendingEvents != 0 {
		t.Fatalf("retired credential status=%+v", got)
	}
	row.CredentialRevision = 2
	if err := store.RecordCommitRelayStatus(ctx, row, "healthy", &pending, &blocked, &oldest); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetCommitSourceEnabled(ctx, source.AccountID, source.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.EnabledSources != 0 || got.PendingEvents != 0 {
		t.Fatalf("paused=%+v", got)
	}
	if _, err := store.SetCommitSourceEnabled(ctx, source.AccountID, source.ID, true); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.UnknownSources != 1 || got.PendingEvents != 0 {
		t.Fatalf("resume reused stale observations=%+v", got)
	}
	if err := store.RecordCommitRelayStatus(ctx, row, "healthy", &pending, &blocked, &oldest); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.UnknownSources != 1 || got.PendingEvents != 0 {
		t.Fatalf("pre-pause pass overwrote resumed health=%+v", got)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM commit_sources WHERE id=$1::uuid`, source.ID); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.EnabledSources != 0 || got.UnknownSources != 0 {
		t.Fatalf("deleted=%+v", got)
	}
}
