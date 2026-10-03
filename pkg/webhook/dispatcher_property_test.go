// Per-account fairness property test (issue #476 / ADR-076).
//
// Property:
//
//	Given N accounts each with equal pending delivery depth and one
//	subscription each, every account can use its four claim slots on
//	every tick without being starved by another account.
//
// This is hand-rolled assertion (not pgregory.net/rapid) to match
// the precedent in pkg/sched/engine_test.go (memory:
// invariants-property-test-fakevmm-reuse).
package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestDispatcher_Fairness_PerAccountRoundRobin drives 10 cycles
// against 5 accounts × 100 pending rows each and asserts the
// max-min deliveries-per-account gap is zero while one subscription
// per account limits each tick to four deliveries.
func TestDispatcher_Fairness_PerAccountRoundRobin(t *testing.T) {
	const (
		accounts   = 5
		perAccount = 100
		ticks      = 10
		cap        = 32
	)

	// Single 200-returning receiver; per-account fairness is about
	// which rows the dispatcher *picks*, not whether they succeed.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	loader, sealed := identityForSealedBlob(t)
	m := state.NewMemStore()
	var accountIDs, webhookIDs []string
	// One webhook per account; deliveries fan-out from it.
	for i := 0; i < accounts; i++ {
		acct := fmt.Sprintf("acct-%d", i)
		appID := fmt.Sprintf("app-%d", i)
		accountIDs = append(accountIDs, acct)
		if _, err := m.CreateApp(context.Background(), state.App{ID: appID, AccountID: acct, Slug: fmt.Sprintf("fairness-app-%d", i), Status: "ready"}); err != nil {
			t.Fatalf("CreateApp[%d]: %v", i, err)
		}
		w := newTestAppWebhook(t, m, appID, acct, srv.URL, state.AppWebhookRetryDefault)
		webhookIDs = append(webhookIDs, w.ID)
		w.SecretSealed = sealed
		if _, err := m.UpdateAppWebhook(context.Background(), w.ID, state.UpdateAppWebhookParams{WebhookSecretSealed: &sealed}); err != nil {
			t.Fatalf("UpdateAppWebhook[%d]: %v", i, err)
		}
		for j := 0; j < perAccount; j++ {
			if _, err := m.RecordAppWebhookDelivery(context.Background(), state.AppWebhookDelivery{
				WebhookID: w.ID,
				AppID:     appID,
				AccountID: acct,
				Event:     "app.cron.fired",
				Payload:   json.RawMessage(`{}`),
			}); err != nil {
				t.Fatalf("RecordAppWebhookDelivery[%d/%d]: %v", i, j, err)
			}
		}
	}

	disp := NewDispatcher(m, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	disp.IdentityLoader = loader
	disp.Sleeper = (&recordingSleeper{}).Sleep
	disp.HTTPClient = srv.Client()
	disp.Cap = cap
	base := time.Now().Add(time.Second)
	var tick int
	disp.Now = func() time.Time { return base.Add(time.Duration(tick) * disp.Tick) }

	// Wait for each cycle's HTTP attempts before measuring its claims.
	var succeededPerAccount [accounts]int
	for tick = 0; tick < ticks; tick++ {
		disp.cycle(context.Background())
		disp.inflight.Wait()
		for i, acct := range accountIDs {
			deliveries, _, err := m.ListAppWebhookDeliveries(context.Background(), fmt.Sprintf("app-%d", i), webhookIDs[i], 0, "")
			if err != nil {
				t.Fatalf("ListAppWebhookDeliveries[%d]: %v", i, err)
			}
			var total int
			for _, d := range deliveries {
				if d.AccountID == acct && d.Status == state.AppWebhookDeliverySucceeded {
					total++
				}
			}
			delta := total - succeededPerAccount[i]
			if delta != state.AppWebhookMaxInFlightPerSubscription {
				t.Errorf("tick %d account %s claimed %d rows, want %d", tick, acct, delta, state.AppWebhookMaxInFlightPerSubscription)
			}
			succeededPerAccount[i] = total
		}
	}

	// Every account remains equally represented over the window.
	var min, max int
	for i := 0; i < accounts; i++ {
		v := succeededPerAccount[i]
		if i == 0 || v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	gap := max - min
	if gap != 0 {
		t.Errorf("fairness gap: got %d, want 0 (min=%d max=%d per-account=%v)",
			gap, min, max, succeededPerAccount)
	}
}
