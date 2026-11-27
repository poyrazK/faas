package webhook

import (
	"context"
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestExecutionCompletionSurvivesDispatcherRestartRetryAndRedrive(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "run-delivery@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	loader, sealed := identityForSealedBlob(t)
	var mu sync.Mutex
	var ids []string
	var eventIDs []string
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		id := r.Header.Get("X-Faas-Delivery-Id")
		stamp, e := strconv.ParseInt(r.Header.Get("X-Faas-Webhook-Timestamp"), 10, 64)
		if e != nil || webhookout.NewSigner([]byte("test-secret")).Verify(stamp, id, body, strings.TrimPrefix(r.Header.Get("X-Faas-Webhook-Signature"), "sha256=")) != nil {
			t.Error("invalid completion signature")
			w.WriteHeader(401)
			return
		}
		var envelope struct {
			Type string                              `json:"type"`
			Data api.ExecutionFinishedWebhookPayload `json:"data"`
		}
		if json.Unmarshal(body, &envelope) != nil || envelope.Type != api.ExecutionFinishedWebhookEvent || strings.Contains(string(body), "PRIVATE_OUTPUT") {
			t.Error("invalid or private notification")
			w.WriteHeader(400)
			return
		}
		mu.Lock()
		ids = append(ids, id)
		eventIDs = append(eventIDs, envelope.Data.EventID)
		attempt := len(ids)
		mu.Unlock()
		switch attempt {
		case 1:
			w.WriteHeader(503)
		case 2:
			w.WriteHeader(400)
		default:
			w.WriteHeader(204)
		}
	}))
	defer receiver.Close()
	limits, _ := api.LimitsFor(account.Plan)
	hook, err := store.CreateAccountReleaseWebhookIfUnderQuota(ctx, state.AppWebhook{AccountID: account.ID, TargetURL: receiver.URL, SecretSealed: sealed, Enabled: true, EventFilter: []string{api.ExecutionFinishedWebhookEvent}, DeliveryFormat: state.AppWebhookDeliveryFormatCloudEvents}, limits)
	if err != nil {
		t.Fatal(err)
	}
	request := api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode24, Source: "export default ()=>42"}
	resolved, p := request.Resolve(api.PlanPro)
	if p != nil {
		t.Fatal(p)
	}
	now := time.Now().UTC()
	run, err := store.CreateExecution(ctx, state.CreateExecutionParams{AccountID: account.ID, Request: resolved, SourceBytes: len(request.Source), AdmittedAt: now, DeadlineAt: now.Add(time.Duration(resolved.Limits.TimeoutMS) * time.Millisecond), SealedPayload: []byte("private"), PayloadKID: "key"})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimExecution(ctx, "worker", now.Add(time.Millisecond), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.MarkExecutionRunning(ctx, run.ID, *claim.LeaseToken, now.Add(2*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CompleteExecution(ctx, state.CompleteExecutionParams{ID: run.ID, LeaseToken: *claim.LeaseToken, Status: api.ExecutionStatusSucceeded, FinishedAt: now.Add(3 * time.Millisecond), Result: json.RawMessage(`"PRIVATE_OUTPUT"`), Stdout: "PRIVATE_OUTPUT"}); err != nil {
		t.Fatal(err)
	}
	rows, _, err := store.ListAccountReleaseWebhookDeliveries(ctx, account.ID, hook.ID, 10, "")
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	deliveryID := rows[0].ID
	dispatch := func() {
		t.Helper()
		d := NewDispatcher(store, nil, slog.New(slog.NewTextHandler(io.Discard, nil))).WithBackoffs(map[state.AppWebhookRetryPolicy][]time.Duration{state.AppWebhookRetryDefault: {time.Millisecond}})
		d.HTTPClient = receiver.Client()
		d.IdentityLoader = loader
		d.cycle(ctx)
		d.inflight.Wait()
	}
	dispatch()
	row, err := store.AppWebhookDeliveryByID(ctx, deliveryID)
	if err != nil || row.Status != state.AppWebhookDeliveryPending || row.Attempt != 1 {
		t.Fatal("retry not durable", row, err)
	}
	time.Sleep(5 * time.Millisecond)
	dispatch() // a fresh dispatcher reads the committed attempt
	row, err = store.AppWebhookDeliveryByID(ctx, deliveryID)
	if err != nil || row.Status != state.AppWebhookDeliveryDead {
		t.Fatal("terminal receiver failure not retained", row, err)
	}
	if err = store.ResetAppWebhookDeliveryFromDead(ctx, deliveryID, hook.ID, account.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	dispatch()
	row, err = store.AppWebhookDeliveryByID(ctx, deliveryID)
	if err != nil || row.Status != state.AppWebhookDeliverySucceeded {
		t.Fatal("redrive failed", row, err)
	}
	runs, err := store.ListExecutions(context.Background(), account.ID, 10, 0)
	if err != nil || len(runs) != 1 || runs[0].ID != run.ID {
		t.Fatal("delivery repeated execution", runs, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(ids) != 3 {
		t.Fatal(ids)
	}
	for i := range ids {
		if ids[i] != deliveryID || eventIDs[i] != run.ID {
			t.Fatal("delivery/event identity changed", ids, eventIDs)
		}
	}
}
