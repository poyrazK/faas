package webhook

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestDispatcher_DeliveriesShareOneConnectionPool is the regression pin for
// schedd's per-delivery connection leak. Production leaves HTTPClient nil,
// and deliverOne builds a webhookout.Dispatcher for every delivery. Each of
// those used to get a fresh transport, so every delivery opened a
// connection that then idled, unreachable, until the receiver closed it.
// Through the production (nil client) path, sequential deliveries to one
// receiver must now reuse a single connection.
func TestDispatcher_DeliveriesShareOneConnectionPool(t *testing.T) {
	t.Setenv("FAAS_EGRESS_ALLOW_LOOPBACK", "1")
	var opened, served atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		served.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			opened.Add(1)
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)

	ctx := context.Background()
	m := state.NewMemStore()
	loader, sealed := identityForSealedBlob(t)
	appID, acctID := "app-pool", "acct-pool"
	if _, err := m.CreateApp(ctx, state.App{ID: appID, AccountID: acctID, Slug: "pool-app", Status: "ready"}); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	w := newTestAppWebhook(t, m, appID, acctID, srv.URL, state.AppWebhookRetryDefault)
	if _, err := m.UpdateAppWebhook(ctx, w.ID, state.UpdateAppWebhookParams{WebhookSecretSealed: &sealed}); err != nil {
		t.Fatalf("UpdateAppWebhook (reseal): %v", err)
	}
	disp := NewDispatcher(m, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	disp.IdentityLoader = loader
	disp.Sleeper = (&recordingSleeper{}).Sleep
	if disp.HTTPClient != nil {
		t.Fatal("NewDispatcher set an HTTPClient; production relies on the shared client")
	}

	const deliveries = 4
	for i := range deliveries {
		if _, err := m.RecordAppWebhookDelivery(ctx, state.AppWebhookDelivery{
			WebhookID: w.ID, AppID: appID, AccountID: acctID, Event: "app.cron.fired",
			Payload: json.RawMessage(`{}`),
		}); err != nil {
			t.Fatalf("RecordAppWebhookDelivery %d: %v", i, err)
		}
		claimed, err := m.ClaimDueAppWebhookDeliveries(ctx, 1, time.Now().Add(time.Second))
		if err != nil || len(claimed) != 1 {
			t.Fatalf("claim %d: %d rows, err %v", i, len(claimed), err)
		}
		disp.deliverOne(ctx, claimed[0])
	}
	if got := served.Load(); got != deliveries {
		t.Fatalf("receiver served %d deliveries, want %d", got, deliveries)
	}
	if got := opened.Load(); got != 1 {
		t.Fatalf("connections opened for %d deliveries = %d, want 1 (one shared pool)", deliveries, got)
	}
}
