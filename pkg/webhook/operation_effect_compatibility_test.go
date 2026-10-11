// adr: 488
package webhook

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/state"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDispatcher_OrdinaryOutboxOperationEffectType(t *testing.T) {
	store := state.NewMemStore()
	ctx := t.Context()
	loader, sealed := identityForSealedBlob(t)
	app, err := store.CreateApp(ctx, state.App{AccountID: "legacy-account", Slug: "legacy-operation-effect"})
	if err != nil {
		t.Fatal(err)
	}
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.Copy(io.Discard, r.Body); w.WriteHeader(204) }))
	defer receiver.Close()
	hook := newTestAppWebhook(t, store, app.ID, app.AccountID, receiver.URL, state.AppWebhookRetryDefault)
	if _, err := store.UpdateAppWebhook(ctx, hook.ID, state.UpdateAppWebhookParams{WebhookSecretSealed: &sealed}); err != nil {
		t.Fatal(err)
	}
	delivery, err := store.RecordAppWebhookDelivery(ctx, state.AppWebhookDelivery{WebhookID: hook.ID, AppID: app.ID, AccountID: app.AccountID, Event: state.OperationEffectEvent, Payload: json.RawMessage(`{"legacy":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	disp := NewDispatcher(store, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	disp.IdentityLoader = loader
	disp.HTTPClient = receiver.Client()
	disp.cycle(ctx)
	deadline := time.Now().Add(2 * time.Second)
	for {
		row, err := store.AppWebhookDeliveryByID(ctx, delivery.ID)
		if err != nil {
			t.Fatal(err)
		}
		if row.Status == state.AppWebhookDeliverySucceeded {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("ordinary outbox regressed: %+v", row)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
