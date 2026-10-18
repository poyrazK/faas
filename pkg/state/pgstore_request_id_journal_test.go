package state_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreRequestIDJournalPersistsAcrossStoreRestartAndScopesLookup(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account, err := store.CreateAccount(ctx, "request-id-journal-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: account.ID, Slug: "rid-" + uuid.NewString()[:8], Type: state.AppTypeApp,
		RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	receivedAt := time.Now().UTC().Truncate(time.Millisecond)
	entry := state.RequestIDJournalEntry{
		ID: uuid.NewString(), AccountID: account.ID, AppID: app.ID,
		RequestID: "customer-supplied-request-id", TraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
		ReceivedAt: receivedAt, ExpiresAt: receivedAt.Add(7 * 24 * time.Hour),
	}
	if err := store.RecordRequestIDJournal(ctx, entry); err != nil {
		t.Fatalf("RecordRequestIDJournal: %v", err)
	}
	// Same record UUID is a safe RPC retry; it must not create a duplicate.
	if err := store.RecordRequestIDJournal(ctx, entry); err != nil {
		t.Fatalf("idempotent RecordRequestIDJournal retry: %v", err)
	}

	// A new PgStore over the same pool models an apid process restart: the ID
	// remains queryable without relying on process-local telemetry buffers.
	restartedStore := state.NewPgStore(pool)
	got, err := restartedStore.FindRequestIDJournalByAppAndIdentifier(
		ctx, account.ID, app.ID, entry.RequestID,
		receivedAt.Add(-time.Second), receivedAt.Add(time.Second), receivedAt,
	)
	if err != nil {
		t.Fatalf("FindRequestIDJournalByAppAndIdentifier after restart: %v", err)
	}
	if got.ID != entry.ID || got.TraceID != entry.TraceID || got.RequestID != entry.RequestID || !got.ReceivedAt.Equal(receivedAt) {
		t.Fatalf("journal lookup = %+v, want %+v", got, entry)
	}
	if _, err := restartedStore.FindRequestIDJournalByAppAndIdentifier(
		ctx, uuid.NewString(), app.ID, entry.RequestID,
		receivedAt.Add(-time.Second), receivedAt.Add(time.Second), receivedAt,
	); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("cross-account lookup err=%v, want pgx.ErrNoRows", err)
	}
	if _, err := restartedStore.FindRequestIDJournalByAppAndIdentifier(
		ctx, account.ID, uuid.NewString(), entry.RequestID,
		receivedAt.Add(-time.Second), receivedAt.Add(time.Second), receivedAt,
	); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("cross-app lookup err=%v, want pgx.ErrNoRows", err)
	}
	if _, err := restartedStore.FindRequestIDJournalByAppAndIdentifier(
		ctx, account.ID, app.ID, entry.RequestID,
		receivedAt.Add(-time.Second), receivedAt.Add(time.Second), entry.ExpiresAt,
	); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expired lookup err=%v, want pgx.ErrNoRows", err)
	}
}

func TestPgStoreRequestIDJournalConcurrentWritesAreAllVisible(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account, err := store.CreateAccount(ctx, "request-id-load-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: account.ID, Slug: "rid-load-" + uuid.NewString()[:8], Type: state.AppTypeApp,
		RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	const requestCount = 48
	receivedAt := time.Now().UTC().Truncate(time.Millisecond)
	errs := make(chan error, requestCount)
	var wg sync.WaitGroup
	for i := range requestCount {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			entry := state.RequestIDJournalEntry{
				ID: uuid.NewString(), AccountID: account.ID, AppID: app.ID,
				RequestID:  fmt.Sprintf("concurrent-request-%02d", i),
				ReceivedAt: receivedAt.Add(time.Duration(i) * time.Millisecond),
				ExpiresAt:  receivedAt.Add(8 * 24 * time.Hour),
			}
			if err := store.RecordRequestIDJournal(ctx, entry); err != nil {
				errs <- fmt.Errorf("record %s: %w", entry.RequestID, err)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	restartedStore := state.NewPgStore(pool)
	for i := range requestCount {
		requestID := fmt.Sprintf("concurrent-request-%02d", i)
		if _, err := restartedStore.FindRequestIDJournalByAppAndIdentifier(
			ctx, account.ID, app.ID, requestID,
			receivedAt.Add(-time.Second), receivedAt.Add(time.Second), receivedAt,
		); err != nil {
			t.Errorf("find %s after concurrent writes: %v", requestID, err)
		}
	}
}

// adr: 1521 — one statement records a whole batch, keeps the ownership check
// and the identical-retry rule per row, and leaves out rows that fail them.
func TestPgStoreRequestIDJournalBatchRecordsOwnedRowsOnly(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	newApp := func(label string) (state.Account, state.App) {
		t.Helper()
		account, err := store.CreateAccount(ctx, "rid-batch-"+label+"-"+uuid.NewString()+"@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		app, err := store.CreateApp(ctx, state.App{
			AccountID: account.ID, Slug: "rid-batch-" + uuid.NewString()[:8], Type: state.AppTypeApp,
			RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60,
		})
		if err != nil {
			t.Fatal(err)
		}
		return account, app
	}
	account, app := newApp("owner")
	_, otherApp := newApp("other")
	receivedAt := time.Now().UTC().Truncate(time.Millisecond)
	entry := func(requestID string, appID string) state.RequestIDJournalEntry {
		return state.RequestIDJournalEntry{
			ID: uuid.NewString(), AccountID: account.ID, AppID: appID, RequestID: requestID,
			ReceivedAt: receivedAt, ExpiresAt: receivedAt.Add(7 * 24 * time.Hour),
		}
	}
	existing := entry("already-recorded", app.ID)
	if err := store.RecordRequestIDJournal(ctx, existing); err != nil {
		t.Fatalf("seed RecordRequestIDJournal: %v", err)
	}
	conflicting := existing
	conflicting.RequestID = "same-id-different-content"
	conflicting.ID = uuid.NewString()
	if err := store.RecordRequestIDJournal(ctx, conflicting); err != nil {
		t.Fatalf("seed conflicting record: %v", err)
	}
	conflicting.RequestID = "rewritten"

	owned1, owned2 := entry("batch-one", app.ID), entry("batch-two", app.ID)
	foreign := entry("not-my-app", otherApp.ID)
	recorded, err := store.RecordRequestIDJournalBatch(ctx, []state.RequestIDJournalEntry{owned1, foreign, existing, owned2, conflicting})
	if err != nil {
		t.Fatalf("RecordRequestIDJournalBatch: %v", err)
	}
	got := map[string]bool{}
	for _, id := range recorded {
		got[id] = true
	}
	want := map[string]bool{owned1.ID: true, owned2.ID: true, existing.ID: true}
	if len(got) != len(want) {
		t.Fatalf("recorded ids = %v, want %v", recorded, want)
	}
	for id := range want {
		if !got[id] {
			t.Fatalf("recorded ids = %v, missing %s", recorded, id)
		}
	}
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM request_id_journal WHERE account_id = $1`, account.ID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 4 { // existing, the seeded conflicting row (unchanged) and the two new rows
		t.Fatalf("journal rows for account = %d, want 4", rows)
	}

	if _, err := store.RecordRequestIDJournalBatch(ctx, []state.RequestIDJournalEntry{owned1, owned1}); err == nil {
		t.Fatal("batch with a repeated record id succeeded, want an error")
	}
}
