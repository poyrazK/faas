package webhook

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 076 — a backlog drains at delivery speed: while claims keep finding
// due rows, each finished delivery claims again at once instead of waiting
// for the next Tick (spec Appendix D V12 measured 6.2 deliveries/s at one
// Cap-sized batch per Tick).
func TestDispatcherDrainsBacklogBetweenTicks(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "webhook-backlog@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "webhook-backlog", Type: state.AppTypeApp,
		RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	var received atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		received.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(target.Close)
	loader, sealed := identityForSealedBlob(t)
	hook, err := store.CreateAppWebhook(ctx, state.AppWebhook{AccountID: account.ID, AppID: app.ID,
		TargetURL: target.URL, SecretSealed: sealed, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	const deliveries = 20
	for range deliveries {
		if _, err := store.RecordAppWebhookDelivery(ctx, state.AppWebhookDelivery{WebhookID: hook.ID, AppID: app.ID,
			AccountID: account.ID, Event: state.AppWebhookEventDeploymentLive, Payload: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}

	d := NewDispatcher(store, nil, slog.New(slog.NewTextHandler(io.Discard, nil))).WithTick(time.Second).WithCap(2)
	d.IdentityLoader, d.HTTPClient = loader, target.Client()
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- d.Run(runCtx) }()
	t.Cleanup(func() {
		stop()
		<-done
	})

	// One Cap-sized batch per Tick would need deliveries/Cap = 10 ticks.
	deadline := time.Now().Add(4 * time.Second)
	for received.Load() < deliveries && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := received.Load(); got != deliveries {
		t.Fatalf("delivered %d of %d within 4 s; a backlog must not wait for the next Tick", got, deliveries)
	}
}
