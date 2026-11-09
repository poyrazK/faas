package webhook

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
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

// adr: 20261101093517 — an empty claim while this dispatcher's deliveries are
// still running may only mean the account or subscription in-flight cap held
// due rows back. It keeps the backlog so the next completion claims again;
// once nothing is running, an empty claim hands pacing back to the Tick.
func TestDispatcherEmptyClaimKeepsBacklogWhileDeliveriesRun(t *testing.T) {
	d := NewDispatcher(state.NewMemStore(), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.backlog, d.active = true, 1
	d.claim(t.Context())
	if !d.backlog {
		t.Fatal("an empty claim with a delivery running cleared the backlog; capped rows would wait for the next Tick")
	}
	d.active = 0
	d.claim(t.Context())
	if d.backlog {
		t.Fatal("an empty claim with no delivery running kept the backlog")
	}
}

// adr: 20261101093517 — one account's backlog held at its in-flight cap
// drains between Ticks. Clearing the backlog on the first claim the cap
// emptied left the rest to a Tick per cap's worth: 18 deliveries/s in V12.
func TestDispatcherDrainsAccountCappedBacklogBetweenTicks(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "webhook-account-cap@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "webhook-account-cap", Type: state.AppTypeApp,
		RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	var received atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(20 * time.Millisecond)
		received.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(target.Close)
	loader, sealed := identityForSealedBlob(t)
	// Eight subscriptions hold 32 deliveries under their own cap, twice the
	// account's 16, so the account cap is the one that empties claims.
	const hooks, perHook = 8, 12
	for i := range hooks {
		hook, err := store.CreateAppWebhook(ctx, state.AppWebhook{AccountID: account.ID, AppID: app.ID,
			TargetURL: target.URL + "/hook-" + strconv.Itoa(i), SecretSealed: sealed, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		for range perHook {
			if _, err := store.RecordAppWebhookDelivery(ctx, state.AppWebhookDelivery{WebhookID: hook.ID, AppID: app.ID,
				AccountID: account.ID, Event: state.AppWebhookEventDeploymentLive, Payload: json.RawMessage(`{}`)}); err != nil {
				t.Fatal(err)
			}
		}
	}

	const tick = 3 * time.Second
	d := NewDispatcher(store, nil, slog.New(slog.NewTextHandler(io.Discard, nil))).WithTick(tick).WithCap(32)
	d.IdentityLoader, d.HTTPClient = loader, target.Client()
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- d.Run(runCtx) }()
	t.Cleanup(func() {
		stop()
		<-done
	})

	// The first Tick starts the drain. From there six cap-sized rounds of
	// 20 ms take well under a second; one wait for the next Tick takes 3 s.
	const deliveries = hooks * perHook
	deadline := time.Now().Add(4 * tick)
	var first time.Time
	for received.Load() < deliveries && time.Now().Before(deadline) {
		if first.IsZero() && received.Load() > 0 {
			first = time.Now()
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := received.Load(); got != deliveries {
		t.Fatalf("delivered %d of %d", got, deliveries)
	}
	if drain := time.Since(first); drain >= 2*time.Second {
		t.Fatalf("an account-capped backlog took %s to drain; it waited for the next Tick", drain)
	}
}
