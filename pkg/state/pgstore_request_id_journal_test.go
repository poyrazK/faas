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
