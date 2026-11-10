package alerts_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

// adr: 20261110090000 — a receiver that kept answering 500 until the
// delivery budget ran out was recorded as "http 500: context deadline
// exceeded", which reads as a receiver timeout. The record names the status
// the receiver returned and that the budget ended the retries.
func TestEvaluator_BudgetEndedRetriesNamesReceiverStatus(t *testing.T) {
	store := state.NewMemStore()
	rule, ident, _ := seedRule(t, store, state.AlertMetricErrorRate, state.AlertGt, 5)
	dispatch := &recordingDispatcher{result: webhookout.Result{
		StatusCode: 500,
		Attempts:   3,
		Err:        fmt.Errorf("webhookout: dispatch: %w", context.DeadlineExceeded),
	}}
	ev, _ := makeEvaluator(t, store, &stubPromQL{value: 10}, ident, dispatch)
	if _, err := ev.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	deliveries, err := store.ListAlertDeliveriesForRule(context.Background(), rule.ID, 5, false)
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("deliveries: err=%v count=%d", err, len(deliveries))
	}
	got := deliveries[0].LastError
	if !strings.Contains(got, "HTTP 500") || !strings.Contains(got, "delivery budget") || strings.Contains(got, "context deadline") {
		t.Fatalf("last_error = %q, want the receiver status and the budget named", got)
	}
}

// adr: 20261110090000 — delivered_at was stamped with the tick's fire time,
// so a delivery that waited behind retries read "delivered at <fire time>".
func TestEvaluator_DeliveredAtIsWhenTheReceiverAccepted(t *testing.T) {
	store := state.NewMemStore()
	rule, ident, _ := seedRule(t, store, state.AlertMetricErrorRate, state.AlertGt, 5)
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 2}}
	// Each clock read is a second later, as a delivery with retries is.
	clock := time.Date(2026, 11, 9, 20, 40, 0, 0, time.UTC)
	ev := alerts.NewEvaluator(alerts.EvaluatorOptions{
		Store:      store,
		PromQL:     &stubPromQL{value: 10},
		Audit:      audit.New(store, discardLog(), nil, "meterd"),
		Identities: func() []*age.X25519Identity { return []*age.X25519Identity{ident} },
		Dispatcher: dispatch,
		Now:        func() time.Time { clock = clock.Add(time.Second); return clock },
		Log:        discardLog(),
	})
	if _, err := ev.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	deliveries, err := store.ListAlertDeliveriesForRule(context.Background(), rule.ID, 5, false)
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("deliveries: err=%v count=%d", err, len(deliveries))
	}
	d := deliveries[0]
	if !d.DeliveredAt.After(d.FiredAt) {
		t.Fatalf("delivered_at = %s, fired_at = %s; want the time the receiver accepted, after the fire", d.DeliveredAt, d.FiredAt)
	}
}
