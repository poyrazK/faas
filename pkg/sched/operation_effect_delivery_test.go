// adr: 488
package sched

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhook"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

func TestManagedOperationEffectDeliveryRetriesWithoutRerunningHandler(t *testing.T) {
	ctx := t.Context()
	drain, base, _, _, _ := newDrainHarness(t, api.PlanPro, false)
	store := base.(*state.MemStore)
	apps, err := store.ListAllApps(ctx)
	if err != nil || len(apps) != 1 {
		t.Fatalf("apps=%+v %v", apps, err)
	}
	app := apps[0]
	ident, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := secretbox.SealBytes(ident.Recipient(), "APP_WEBHOOK", []byte("receiver-secret"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var deliveryIDs []string
	var payloads []api.OperationEffectPayload
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		id := r.Header.Get("X-Faas-Delivery-Id")
		unix, err := strconv.ParseInt(r.Header.Get("X-Faas-Webhook-Timestamp"), 10, 64)
		if err != nil || webhookout.NewSigner([]byte("receiver-secret")).Verify(unix, id, body, strings.TrimPrefix(r.Header.Get("X-Faas-Webhook-Signature"), "sha256=")) != nil {
			t.Error("invalid signed delivery")
			w.WriteHeader(401)
			return
		}
		var event struct {
			Type string                     `json:"type"`
			Data api.OperationEffectPayload `json:"data"`
		}
		if err := json.Unmarshal(body, &event); err != nil || event.Type != state.OperationEffectEvent {
			t.Errorf("event=%s err=%v", body, err)
			w.WriteHeader(400)
			return
		}
		mu.Lock()
		deliveryIDs = append(deliveryIDs, id)
		payloads = append(payloads, event.Data)
		attempt := len(deliveryIDs)
		mu.Unlock()
		if attempt == 1 {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(204)
	}))
	defer receiver.Close()
	hook, err := store.CreateAppWebhook(ctx, state.AppWebhook{AccountID: app.AccountID, AppID: app.ID, TargetURL: receiver.URL, SecretSealed: sealed, EventFilter: []string{state.OperationEffectEvent}, DeliveryFormat: state.AppWebhookDeliveryFormatCloudEvents, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	owners := state.ExclusiveWorkStore(store)
	if _, err := owners.UpsertExclusiveWorkPolicy(ctx, app.AccountID, exclusivework.Policy{Name: "orders", Scope: "account", MemberAppIDs: []string{app.ID}, Contention: "queue", LeaseSeconds: 5, MaxAttemptSeconds: 30}); err != nil {
		t.Fatal(err)
	}
	op, _, err := owners.AdmitExclusiveOperation(ctx, state.ExclusiveAdmission{AccountID: app.AccountID, AppID: app.ID, PolicyName: "orders", Key: json.RawMessage(`"order-123"`), Request: json.RawMessage(`{"method":"POST","path":"/orders"}`)})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(api.ManagedOperationResult{Version: 1, Result: json.RawMessage(`{"order_id":123}`), Effects: []api.ManagedOperationEffect{{Name: "customer-notification", WebhookID: hook.ID, Type: "order.fulfilled", Payload: json.RawMessage(`{"order_id":123}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	gateway := &exclusiveDrainGateway{result: body}
	drain.gateway = gateway
	drain.Tick(ctx)
	completed, err := owners.ExclusiveOperationByID(ctx, app.AccountID, op.ID)
	if err != nil || completed.State != "completed" || string(completed.Result) != `{"order_id":123}` || len(completed.Effects) != 1 {
		t.Fatalf("completion=%+v err=%v", completed, err)
	}
	deliveryID := completed.Effects[0].DeliveryID
	// Starting a fresh dispatcher after completion simulates a scheduler restart.
	disp := webhook.NewDispatcher(store, nil, slog.New(slog.NewTextHandler(io.Discard, nil))).WithTick(2 * time.Millisecond).WithBackoffs(map[state.AppWebhookRetryPolicy][]time.Duration{state.AppWebhookRetryDefault: {5 * time.Millisecond}})
	disp.HTTPClient = receiver.Client()
	disp.IdentityLoader = func() []*age.X25519Identity { return []*age.X25519Identity{ident} }
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- disp.Run(runCtx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		completed, err = owners.ExclusiveOperationByID(ctx, app.AccountID, op.ID)
		if err != nil {
			t.Fatal(err)
		}
		if completed.Effects[0].Status == "succeeded" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("delivery never succeeded: %+v", completed.Effects)
		}
		time.Sleep(5 * time.Millisecond)
	}
	drain.Tick(ctx)
	if gateway.calls != 1 {
		t.Fatalf("handler reran after completion: %d", gateway.calls)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(deliveryIDs) != 2 || deliveryIDs[0] != deliveryID || deliveryIDs[1] != deliveryID || completed.Effects[0].Attempt != 2 {
		t.Fatalf("retry identities=%v receipt=%+v", deliveryIDs, completed.Effects)
	}
	for _, payload := range payloads {
		if payload.OperationID != op.ID || payload.Name != "customer-notification" || payload.Type != "order.fulfilled" || string(payload.Data) != `{"order_id":123}` {
			t.Fatalf("payload=%+v", payload)
		}
	}
}

func TestManagedOperationEffectInvalidEnvelopeFailsWithoutDelivery(t *testing.T) {
	for _, response := range []string{
		`{"gregale_operation_result":2,"result":{},"effects":[]}`,
		`{"gregale_operation_result":1,"result":{},"effects":[{"name":"notify","webhook_id":"00000000-0000-0000-0000-000000000000","type":"order.fulfilled","payload":{}}]}`,
	} {
		t.Run(response, func(t *testing.T) {
			drain, base, _, _, _ := newDrainHarness(t, api.PlanPro, false)
			store := base.(*state.MemStore)
			ctx := t.Context()
			apps, err := store.ListAllApps(ctx)
			if err != nil {
				t.Fatal(err)
			}
			app := apps[0]
			owners := state.ExclusiveWorkStore(store)
			if _, err := owners.UpsertExclusiveWorkPolicy(ctx, app.AccountID, exclusivework.Policy{Name: "orders", Scope: "account", MemberAppIDs: []string{app.ID}, Contention: "queue", LeaseSeconds: 5, MaxAttemptSeconds: 30}); err != nil {
				t.Fatal(err)
			}
			op, _, err := owners.AdmitExclusiveOperation(ctx, state.ExclusiveAdmission{AccountID: app.AccountID, AppID: app.ID, PolicyName: "orders", Key: json.RawMessage(`"orders"`), Request: json.RawMessage(`{"method":"POST","path":"/orders"}`)})
			if err != nil {
				t.Fatal(err)
			}
			gateway := &exclusiveDrainGateway{result: json.RawMessage(response)}
			drain.gateway = gateway
			drain.Tick(ctx)
			failed, err := owners.ExclusiveOperationByID(ctx, app.AccountID, op.ID)
			if err != nil || failed.State != "failed" || len(failed.Effects) != 0 || len(failed.Result) != 0 {
				t.Fatalf("invalid response completion=%+v %v", failed, err)
			}
		})
	}
}
