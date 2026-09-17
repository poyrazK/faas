// adr: 179

// Unified dead-letter E2E coverage is intentionally narrow. The Store
// conformance suite already exercises the invocation source; these tests
// keep the production PostgreSQL trigger-record source and the HTTP replay
// boundary from drifting apart.

package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestE2E_UnifiedDeadLetter_TriggerRecordReplay proves the broker-trigger
// source is captured by PostgreSQL and can be replayed through the customer
// API. It also verifies that replay removes the source DLQ row and that a
// later terminal failure re-opens the same unified event instead of creating
// a duplicate history row.
func TestE2E_UnifiedDeadLetter_TriggerRecordReplay(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ctx := context.Background()
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(ctx, api.PlanPro, "dlq-trigger-replay")
	appID, slug := createDLQTestApp(t, h, key, "dlq-trigger-replay")
	store := state.NewPgStore(h.Pool)
	triggerID, recordID := seedTriggerDeadLetter(t, ctx, store, appID, "first")

	body, status := doReq(t, h, key, http.MethodGet, "/v1/apps/"+slug+"/dlq", nil)
	if status != http.StatusOK {
		t.Fatalf("list status=%d want 200: %s", status, body)
	}
	var page api.DeadLetterEventsResponse
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode list: %v; body=%s", err, body)
	}
	if len(page.Events) != 1 {
		t.Fatalf("events=%d want 1: %+v", len(page.Events), page.Events)
	}
	event := page.Events[0]
	if event.Source != "trigger_record" || event.SourceID != recordID {
		t.Fatalf("source=%q/%q want trigger_record/%s", event.Source, event.SourceID, recordID)
	}
	if event.TriggerID != triggerID {
		t.Fatalf("trigger_id=%q want %q", event.TriggerID, triggerID)
	}
	if string(event.Payload) != `{"order_id":"first"}` {
		t.Fatalf("payload=%s want original trigger payload", event.Payload)
	}
	if event.ErrorKind != "poison_record" || event.RetryCount != 1 {
		t.Fatalf("failure metadata=%q/%d want poison_record/1", event.ErrorKind, event.RetryCount)
	}

	body, status = doReq(t, h, key, http.MethodPost,
		"/v1/apps/"+slug+"/dlq/"+event.ID+"/replay", nil,
		map[string]string{"Idempotency-Key": "dlq-trigger-replay-1"})
	if status != http.StatusAccepted {
		t.Fatalf("replay status=%d want 202: %s", status, body)
	}
	var replayed api.DeadLetterEvent
	if err := json.Unmarshal(body, &replayed); err != nil {
		t.Fatalf("decode replay: %v; body=%s", err, body)
	}
	if replayed.ReplayedAt == nil {
		t.Fatal("replayed_at is nil")
	}

	records, err := store.ListTriggerRecordsForTrigger(ctx, triggerID, 10)
	if err != nil {
		t.Fatalf("ListTriggerRecordsForTrigger: %v", err)
	}
	if len(records) != 1 || records[0].State != "pending" || records[0].Attempts != 0 {
		t.Fatalf("record after replay=%+v want pending with zero attempts", records)
	}
	dlqRows, err := store.ListTriggerDeadLetter(ctx, triggerID, 10)
	if err != nil {
		t.Fatalf("ListTriggerDeadLetter: %v", err)
	}
	if len(dlqRows) != 0 {
		t.Fatalf("trigger DLQ rows after replay=%d want 0", len(dlqRows))
	}

	// The same source record can fail again after replay. The trigger on
	// trigger_dead_letter must reopen the existing unified event (the unique
	// source/source_id key), not silently create a second event or leave it
	// marked as already replayed.
	if err := store.MarkTriggerRecordDeadLetter(ctx, recordID, "failed again"); err != nil {
		t.Fatalf("MarkTriggerRecordDeadLetter(second): %v", err)
	}
	if err := store.InsertTriggerDeadLetter(ctx, recordID, triggerID, "poison_record", "customer_dlq", []byte(`{"attempt":2}`)); err != nil {
		t.Fatalf("InsertTriggerDeadLetter(second): %v", err)
	}
	events, err := store.ListDeadLetterEvents(ctx, appID, 10, "")
	if err != nil {
		t.Fatalf("ListDeadLetterEvents(after second failure): %v", err)
	}
	if len(events) != 1 || events[0].ID != event.ID || events[0].ReplayedAt != nil || events[0].RetryCount != 1 {
		t.Fatalf("events after second failure=%+v want one reopened event", events)
	}
}

// TestE2E_UnifiedDeadLetter_ConcurrentReplayIsSingleWinner exercises the
// row lock and replayed_at guard with two real PostgreSQL callers. Exactly
// one caller may reset the source; the loser must see ErrNotFound after the
// winner commits.
func TestE2E_UnifiedDeadLetter_ConcurrentReplayIsSingleWinner(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ctx := context.Background()
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(ctx, api.PlanPro, "dlq-replay-race")
	appID, _ := createDLQTestApp(t, h, key, "dlq-replay-race")
	store := state.NewPgStore(h.Pool)
	triggerID, _ := seedTriggerDeadLetter(t, ctx, store, appID, "race")
	events, err := store.ListDeadLetterEvents(ctx, appID, 10, "")
	if err != nil || len(events) != 1 {
		t.Fatalf("seeded events=%+v err=%v", events, err)
	}
	app, err := store.AppByID(ctx, appID)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}

	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, replayErr := store.ReplayDeadLetterEvent(ctx, app.AccountID, appID, events[0].ID)
			results <- replayErr
		}()
	}
	wg.Wait()
	close(results)

	var winners, notFound int
	for replayErr := range results {
		switch {
		case replayErr == nil:
			winners++
		case errors.Is(replayErr, state.ErrNotFound):
			notFound++
		default:
			t.Fatalf("concurrent replay error=%v want nil or ErrNotFound", replayErr)
		}
	}
	if winners != 1 || notFound != 1 {
		t.Fatalf("concurrent replay results: winners=%d not_found=%d want 1/1", winners, notFound)
	}

	records, err := store.ListTriggerRecordsForTrigger(ctx, triggerID, 10)
	if err != nil {
		t.Fatalf("ListTriggerRecordsForTrigger: %v", err)
	}
	if len(records) != 1 || records[0].State != "pending" || records[0].Attempts != 0 {
		t.Fatalf("record after concurrent replay=%+v want pending/0", records)
	}
}

// TestE2E_UnifiedDeadLetter_PaginationUsesStableCursor forces equal failure
// timestamps so the UUID tie-breaker is required. The two API pages must be
// disjoint and cover every event.
func TestE2E_UnifiedDeadLetter_PaginationUsesStableCursor(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ctx := context.Background()
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(ctx, api.PlanPro, "dlq-pagination")
	appID, slug := createDLQTestApp(t, h, key, "dlq-pagination")
	store := state.NewPgStore(h.Pool)
	for _, item := range []string{"page-1", "page-2", "page-3"} {
		seedTriggerDeadLetter(t, ctx, store, appID, item)
	}

	// Make the primary ordering key identical for every row. A query that
	// only compares timestamps will duplicate or skip one of these events.
	if _, err := h.Pool.Exec(ctx,
		`update dead_letter_events set last_failed_at = $1 where app_id = $2`,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), appID); err != nil {
		t.Fatalf("equalize failure timestamps: %v", err)
	}

	body, status := doReq(t, h, key, http.MethodGet, "/v1/apps/"+slug+"/dlq?limit=2", nil)
	if status != http.StatusOK {
		t.Fatalf("page 1 status=%d: %s", status, body)
	}
	var first api.DeadLetterEventsResponse
	if err := json.Unmarshal(body, &first); err != nil {
		t.Fatalf("decode page 1: %v", err)
	}
	if len(first.Events) != 2 || first.NextBefore == "" {
		t.Fatalf("page 1=%+v want two events and cursor", first)
	}

	body, status = doReq(t, h, key, http.MethodGet,
		"/v1/apps/"+slug+"/dlq?limit=2&before="+url.QueryEscape(first.NextBefore), nil)
	if status != http.StatusOK {
		t.Fatalf("page 2 status=%d: %s", status, body)
	}
	var second api.DeadLetterEventsResponse
	if err := json.Unmarshal(body, &second); err != nil {
		t.Fatalf("decode page 2: %v", err)
	}
	if len(second.Events) != 1 {
		t.Fatalf("page 2 events=%d want 1: %+v", len(second.Events), second.Events)
	}

	seen := map[string]bool{}
	for _, event := range append(first.Events, second.Events...) {
		if seen[event.ID] {
			t.Fatalf("event %s repeated across cursor pages", event.ID)
		}
		seen[event.ID] = true
	}
	if len(seen) != 3 {
		t.Fatalf("events across pages=%d want 3", len(seen))
	}
}

func createDLQTestApp(t *testing.T, h *e2etest.Harness, key, slug string) (string, string) {
	t.Helper()
	body, status := doReq(t, h, key, http.MethodPost, "/v1/apps", api.CreateAppRequest{
		Slug: slug,
		Type: string(state.AppTypeApp),
	})
	if status != http.StatusCreated {
		t.Fatalf("create app status=%d: %s", status, body)
	}
	var app api.AppResponse
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatalf("decode app: %v; body=%s", err, body)
	}
	return app.ID, app.Slug
}

func seedTriggerDeadLetter(t *testing.T, ctx context.Context, store *state.PgStore, appID, item string) (string, string) {
	t.Helper()
	trigger, err := store.CreateTriggerIfUnderQuota(
		ctx, appID, "kafka", "orders-"+item, true,
		[]byte(`{"brokers":["127.0.0.1:9092"]}`), "", 10, 1000, 3, 1024,
		"commit", api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatalf("CreateTriggerIfUnderQuota: %v", err)
	}
	triggerID := trigger.ID.String()
	recordID, err := store.InsertTriggerRecord(ctx, triggerID, item,
		[]byte(`{"order_id":"`+item+`"}`), []byte(`{"x-test":"dlq"}`), []byte(`{"source":"e2e"}`))
	if err != nil {
		t.Fatalf("InsertTriggerRecord: %v", err)
	}
	if err := store.MarkTriggerRecordDeadLetter(ctx, recordID, "poison payload"); err != nil {
		t.Fatalf("MarkTriggerRecordDeadLetter: %v", err)
	}
	if err := store.InsertTriggerDeadLetter(ctx, recordID, triggerID,
		"poison_record", "customer_dlq", []byte(`{"reason":"e2e"}`)); err != nil {
		t.Fatalf("InsertTriggerDeadLetter: %v", err)
	}
	return triggerID, recordID
}
