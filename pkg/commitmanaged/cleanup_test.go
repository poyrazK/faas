package commitmanaged

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	commitwork "github.com/onebox-faas/faas/pkg/commit"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCommitCleanupCatchesUpWhileDelivering(t *testing.T) {
	e := newChangesEnv(t, true)
	ctx := t.Context()
	if _, err := e.customer.Exec(ctx, `INSERT INTO public.orders VALUES (1, 'initial', NULL)`); err != nil {
		t.Fatal(err)
	}
	if err := e.manager.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	var initialID string
	if err := e.customer.QueryRow(ctx, `SELECT event_id::text FROM public.gregale_outbox WHERE group_key='orders:{"id": 1}'`).Scan(&initialID); err != nil {
		t.Fatal(err)
	}
	original, err := e.store.CommitReceiptByEvent(ctx, e.account.ID, e.source.ID, initialID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.customer.Exec(ctx, `UPDATE public.gregale_outbox SET accepted_at=clock_timestamp()-interval '8 days' WHERE event_id=$1::uuid`, initialID); err != nil {
		t.Fatal(err)
	}
	// The rate of expired rows exceeds the former 32-row per-pass cleanup.
	if _, err := e.customer.Exec(ctx, `INSERT INTO public.gregale_outbox(event_id,event_type,payload,accepted_at,receipt_id,invocation_id)
 SELECT gen_random_uuid(),'order.old','{}',clock_timestamp()-interval '8 days',gen_random_uuid(),gen_random_uuid()
 FROM generate_series(1,400)`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.customer.Exec(ctx, `INSERT INTO public.orders VALUES (2,'blocked',NULL),(3,'new',NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.customer.Exec(ctx, `UPDATE public.gregale_outbox SET blocked_code='invalid_event' WHERE group_key='orders:{"id": 2}'`); err != nil {
		t.Fatal(err)
	}
	var deleted int64
	e.manager.ReportCleanup = func(rows int64, _ time.Duration, _ string) { deleted += rows }
	for i := range 10 {
		e.makeDue(t)
		if err := e.manager.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		var remaining int
		if err := e.customer.QueryRow(ctx, `SELECT count(*) FROM public.gregale_outbox WHERE accepted_at<clock_timestamp()-interval '7 days'`).Scan(&remaining); err != nil {
			t.Fatal(err)
		}
		if remaining == 0 {
			break
		}
		if i == 9 {
			t.Fatalf("%d expired accepted rows remain after ten passes", remaining)
		}
	}
	if deleted != 401 {
		t.Fatalf("cleanup reported %d deleted rows, want 401", deleted)
	}
	var newAccepted, blockedPreserved bool
	if err := e.customer.QueryRow(ctx, `SELECT
 EXISTS(SELECT 1 FROM public.gregale_outbox WHERE group_key='orders:{"id": 3}' AND accepted_at IS NOT NULL),
 EXISTS(SELECT 1 FROM public.gregale_outbox WHERE group_key='orders:{"id": 2}' AND accepted_at IS NULL AND blocked_code='invalid_event')`).Scan(&newAccepted, &blockedPreserved); err != nil || !newAccepted || !blockedPreserved {
		t.Fatalf("new accepted=%t blocked preserved=%t: %v", newAccepted, blockedPreserved, err)
	}
	var exists bool
	if err := e.customer.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.gregale_outbox WHERE event_id=$1::uuid)`, initialID).Scan(&exists); err != nil || exists {
		t.Fatalf("expired accepted event still present=%t: %v", exists, err)
	}
	recovered, err := e.store.CommitReceiptByEvent(ctx, e.account.ID, e.source.ID, initialID)
	if err != nil || recovered.ID != original.ID || recovered.InvocationID != original.InvocationID {
		t.Fatalf("receipt after cleanup=%+v, before=%+v: %v", recovered, original, err)
	}
	var eventType string
	var payload []byte
	if err := e.control.QueryRow(ctx, `SELECT event_type,payload FROM commit_receipts WHERE id=$1::uuid`, original.ID).Scan(&eventType, &payload); err != nil {
		t.Fatal(err)
	}
	replayed, err := e.store.AcceptCommitChange(ctx, e.account.ID, e.source.ID, initialID, eventType, payload, state.Invocation{}, nil)
	if err != nil || replayed.ID != original.ID {
		t.Fatalf("retry after customer cleanup=%+v: %v", replayed, err)
	}
}

func TestCommitCleanupTimeoutPreservesDeliveryAndHealth(t *testing.T) {
	e := newChangesEnv(t, true)
	ctx := t.Context()
	type report struct {
		rows     int64
		duration time.Duration
		result   string
	}
	reports := make(chan report, 1)
	e.manager.ReportCleanup = func(rows int64, duration time.Duration, result string) {
		reports <- report{rows, duration, result}
	}
	if _, err := e.customer.Exec(ctx, `INSERT INTO public.gregale_outbox(event_id,event_type,payload,accepted_at,receipt_id,invocation_id)
 VALUES (gen_random_uuid(),'order.old','{}',clock_timestamp()-interval '8 days',gen_random_uuid(),gen_random_uuid());
 CREATE FUNCTION public.delay_outbox_delete() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN PERFORM pg_sleep(5); RETURN OLD; END $$;
 CREATE TRIGGER delay_outbox_delete BEFORE DELETE ON public.gregale_outbox
 FOR EACH ROW EXECUTE FUNCTION public.delay_outbox_delete();
 INSERT INTO public.orders VALUES (1, 'new', NULL)`); err != nil {
		t.Fatal(err)
	}
	if err := e.manager.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if got := <-reports; got.rows != 0 || got.result != "time_limit" || got.duration < relayCleanupBudget-100*time.Millisecond {
		t.Fatalf("cleanup timeout metrics: %+v", got)
	}
	if got := e.delivered(t); len(got) != 1 || got[0] != "1:new" {
		t.Fatalf("delivery during slow cleanup: %v", got)
	}
	var oldCount int
	if err := e.customer.QueryRow(ctx, `SELECT count(*) FROM public.gregale_outbox WHERE event_type='order.old'`).Scan(&oldCount); err != nil || oldCount != 1 {
		t.Fatalf("timed-out cleanup removed accepted rows: count=%d err=%v", oldCount, err)
	}
	source, err := e.store.CommitSourceByID(ctx, e.account.ID, e.source.ID)
	if err != nil || source.RelayStatus != "healthy" || source.PendingEvents == nil || *source.PendingEvents != 0 {
		t.Fatalf("source observation after cleanup budget: %+v err=%v", source, err)
	}
	snapshot, err := e.store.CommitRelayObservationSummary(ctx, time.Now().Add(-commitwork.ObservationGracePeriod))
	if err != nil || snapshot.CleanupEligibleEvents != 1 || snapshot.CleanupUnknownSources != 0 {
		t.Fatalf("cleanup backlog after timeout: %+v err=%v", snapshot, err)
	}
	e.manager.Open = func(context.Context, string, commitwork.NetworkPolicy) (*pgxpool.Pool, error) {
		return nil, errors.New("fixture database unavailable")
	}
	e.makeDue(t)
	if err := e.manager.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	snapshot, err = e.store.CommitRelayObservationSummary(ctx, time.Now().Add(-commitwork.ObservationGracePeriod))
	if err != nil || snapshot.CleanupEligibleEvents != 0 || snapshot.CleanupUnknownSources != 1 {
		t.Fatalf("unreachable source cleanup observation: %+v err=%v", snapshot, err)
	}
}
