package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

func TestScenarioActionDispatcherNeverTransmitsCapturedAppWebhooks(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "capture-dispatch@example.invalid", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "capture-app", PreviewOfSlug: "source-app", PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expires})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterScenarioTestMembers(ctx, account.ID, "abababababababababababababababab", []state.ScenarioTestMember{{AppID: app.ID, Workload: "primary", WebhookMode: state.ScenarioWebhookCaptureMode}}); err != nil {
		t.Fatal(err)
	}
	var sent atomic.Int64
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { sent.Add(1); w.WriteHeader(204) }))
	defer receiver.Close()
	hook := newTestAppWebhook(t, store, app.ID, account.ID, receiver.URL, state.AppWebhookRetryDefault)
	loader, sealed := identityForSealedBlob(t)
	if _, err := store.UpdateAppWebhook(ctx, hook.ID, state.UpdateAppWebhookParams{WebhookSecretSealed: &sealed}); err != nil {
		t.Fatal(err)
	}
	dispatcher := NewDispatcher(store, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	dispatcher.IdentityLoader = loader
	dispatcher.HTTPClient = receiver.Client()
	for _, event := range []string{state.OperationEffectEvent, string(state.AppWebhookEventDeploymentLive)} {
		row, err := store.RecordAppWebhookDelivery(ctx, state.AppWebhookDelivery{AppID: app.ID, AccountID: account.ID, WebhookID: hook.ID, Event: state.AppWebhookEvent(event), Payload: json.RawMessage(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		// Even a stale worker holding an apparent in-flight row must consult
		// the retained policy before loading secrets or performing HTTP.
		row.Status = state.AppWebhookDeliveryInFlight
		dispatcher.deliverOne(ctx, row)
		row.AppID = "" // Receiver scope also fences ordinary app events.
		dispatcher.deliverOne(ctx, row)
	}
	if _, err := store.SoftDeleteAppCascade(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteScenarioTestMembers(ctx, account.ID, "abababababababababababababababab"); err != nil {
		t.Fatal(err)
	}
	dispatcher.deliverOne(ctx, state.AppWebhookDelivery{ID: "retained", AppID: app.ID, WebhookID: hook.ID, Event: state.OperationEffectEvent})
	if sent.Load() != 0 {
		t.Fatalf("rehearsal transmitted %d external requests", sent.Load())
	}
}

type scenarioPolicyUnavailable struct{ MemStore *state.MemStore }

func (s *scenarioPolicyUnavailable) ScenarioWebhookDeliveryBlocked(context.Context, string, string) (bool, error) {
	return false, errors.New("capture authority unavailable")
}

func TestScenarioActionDispatcherFailsClosedWhenPolicyReadFails(t *testing.T) {
	store := state.NewMemStore()
	ctx := t.Context()
	app, err := store.CreateApp(ctx, state.App{AccountID: "legacy-account", Slug: "policy-read-failure"})
	if err != nil {
		t.Fatal(err)
	}
	var sent atomic.Int64
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { sent.Add(1); w.WriteHeader(204) }))
	defer receiver.Close()
	hook := newTestAppWebhook(t, store, app.ID, app.AccountID, receiver.URL, state.AppWebhookRetryDefault)
	loader, sealed := identityForSealedBlob(t)
	if _, err := store.UpdateAppWebhook(ctx, hook.ID, state.UpdateAppWebhookParams{WebhookSecretSealed: &sealed}); err != nil {
		t.Fatal(err)
	}
	row, err := store.RecordAppWebhookDelivery(ctx, state.AppWebhookDelivery{AppID: app.ID, AccountID: app.AccountID, WebhookID: hook.ID, Event: state.AppWebhookEventDeploymentLive, Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.ClaimDueAppWebhookDeliveries(ctx, 1, time.Now().Add(time.Second))
	if err != nil || len(rows) != 1 {
		t.Fatalf("claim: %+v %v", rows, err)
	}
	dispatcher := NewDispatcher(statetest.NewMem(&scenarioPolicyUnavailable{MemStore: store}), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	dispatcher.IdentityLoader = loader
	dispatcher.HTTPClient = receiver.Client()
	dispatcher.deliverOne(ctx, rows[0])
	after, err := store.AppWebhookDeliveryByID(ctx, row.ID)
	if err != nil || after.Status != state.AppWebhookDeliveryInFlight || sent.Load() != 0 {
		t.Fatalf("unknown policy transmitted or consumed claim: %+v %v calls=%d", after, err, sent.Load())
	}
}
