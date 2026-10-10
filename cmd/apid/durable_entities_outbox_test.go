// adr: 843
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/onebox-faas/faas/pkg/state"
)

func entityOutboxHook(t *testing.T, e entityHTTPEnv, app state.App) state.AppWebhook {
	t.Helper()
	hook, err := e.store.CreateAppWebhook(t.Context(), state.AppWebhook{ID: uuid.NewString(), AccountID: e.acct.ID, AppID: app.ID, TargetURL: "https://receiver.example.test/" + uuid.NewString(), SecretSealed: []byte("sealed"), Enabled: true, RetryPolicy: state.AppWebhookRetryDefault})
	if err != nil {
		t.Fatal(err)
	}
	return hook
}

func seedAPIOutbox(t *testing.T, e entityHTTPEnv, app state.App, request api.DurableEntityInvokeRequest, hookID string) durableentity.OutboxWork {
	t.Helper()
	id, _, problem := e.s.durableEntityIdentity((&http.Request{}).WithContext(t.Context()), e.acct, app, request)
	if problem != nil {
		t.Fatal(problem)
	}
	_, err := e.s.durableEntities.Invoke(t.Context(), id, uuid.NewString(), durableentity.Request{ID: request.RequestID, Payload: request.Payload}, func(context.Context, durableentity.View) (durableentity.Transition, error) {
		return durableentity.Transition{Data: json.RawMessage(`{"count":1}`), Result: json.RawMessage(`{"count":1}`), Outbox: []durableentity.OutboxIntent{{WebhookID: hookID, EventType: "reservation.confirmed", Payload: json.RawMessage(`{"reservation":"123"}`)}}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := e.s.durableEntities.PendingOutbox(t.Context(), id)
	if err != nil || len(view.Messages) != 1 {
		t.Fatal(view, err)
	}
	return durableentity.OutboxWork{Entity: id, MessageID: view.Messages[0].ID}
}

type lostEntityOutboxAcceptanceStore struct {
	*state.MemStore
	lose atomic.Bool
}

func (s *lostEntityOutboxAcceptanceStore) AcceptEntityOutboxDelivery(ctx context.Context, in state.AppWebhookDelivery) (string, error) {
	id, err := s.MemStore.AcceptEntityOutboxDelivery(ctx, in)
	if err == nil && s.lose.Swap(false) {
		return "", errors.New("injected lost SQL commit acknowledgement with private detail")
	}
	return id, err
}

func TestDurableEntityOutboxRestartsAfterLostAcceptanceAcknowledgement(t *testing.T) {
	e, app, dispatch, bucket := entityAPIFixture(t, true)
	clock := &atomic.Int64{}
	clock.Store(time.Now().UTC().UnixNano())
	now := func() time.Time { return time.Unix(0, clock.Load()) }
	engine, err := durableentity.Open(t.Context(), bucket, durableentity.Options{Now: now, LeaseDuration: api.MaxDurableEntityLease})
	if err != nil {
		t.Fatal(err)
	}
	e.s.durableEntities, e.s.durableEntityOutboxEnabled = engine, true
	hook := entityOutboxHook(t, e, app)
	request := entityRequest("confirmation")
	request.Environment = "staging"
	work := seedAPIOutbox(t, e, app, request, hook.ID)
	store := &lostEntityOutboxAcceptanceStore{MemStore: e.store}
	store.lose.Store(true)
	e.s.store = store
	if err := e.s.deliverDurableEntityOutbox(t.Context(), work); err == nil {
		t.Fatal("lost acceptance ACK reported success")
	}
	status, err := engine.InspectOutbox(t.Context(), work.Entity)
	if err != nil || status.Pending != 1 || status.Attempts != 1 {
		t.Fatal(status, err)
	}
	// A business transition preserves both the pending head and relay reservation.
	if _, err := engine.Invoke(t.Context(), work.Entity, uuid.NewString(), durableentity.Request{ID: "ordinary", Payload: json.RawMessage(`null`)}, func(context.Context, durableentity.View) (durableentity.Transition, error) {
		return durableentity.Transition{Data: json.RawMessage(`{"count":2}`), Result: json.RawMessage(`null`)}, nil
	}); err != nil {
		t.Fatal(err)
	}
	clock.Add(int64(api.MaxDurableEntityLease + time.Second))
	restarted, err := durableentity.Open(t.Context(), bucket, durableentity.Options{Now: now, LeaseDuration: api.MaxDurableEntityLease})
	if err != nil {
		t.Fatal(err)
	}
	e.s.durableEntities, e.s.durableEntityOwner = restarted, uuid.NewString()
	if _, err := e.s.sweepDurableEntityOutbox(t.Context(), outboxSweepCursor{}); err != nil {
		t.Fatal(err)
	}
	rows, _, err := e.store.ListAppWebhookDeliveries(t.Context(), app.ID, hook.ID, 100, "")
	if err != nil || len(rows) != 1 || rows[0].ID != work.MessageID || rows[0].Event != "reservation.confirmed" {
		t.Fatal(rows, err)
	}
	var body struct {
		Entity    durableentity.ID `json:"entity"`
		MessageID string           `json:"message_id"`
		Version   uint64           `json:"state_version"`
		Data      json.RawMessage  `json:"data"`
	}
	if err := json.Unmarshal(rows[0].Payload, &body); err != nil || body.Entity != work.Entity || body.MessageID != work.MessageID || body.Version != 1 || string(body.Data) != `{"reservation":"123"}` {
		t.Fatal(body, err)
	}
	status, err = restarted.InspectOutbox(t.Context(), work.Entity)
	if err != nil || status.Pending != 0 || status.Version != 2 || status.Attempts != 0 || dispatch.calls.Load() != 0 {
		t.Fatal(status, err)
	}
	result, err := restarted.Invoke(t.Context(), work.Entity, uuid.NewString(), durableentity.Request{ID: request.RequestID, Payload: request.Payload}, func(context.Context, durableentity.View) (durableentity.Transition, error) {
		t.Fatal("replay reran callback")
		return durableentity.Transition{}, nil
	})
	if err != nil || !result.Replayed || result.Version != 1 {
		t.Fatal(result, err)
	}
}

func TestDurableEntityOutboxRechecksScopeAdmissionAndDestination(t *testing.T) {
	for _, kind := range []string{"account", "abuse", "plan", "app", "allowlist", "disabled", "tenant", "environment", "destination", "foreign-destination"} {
		t.Run(kind, func(t *testing.T) {
			e, app, dispatch, _ := entityAPIFixture(t, kind == "environment")
			e.s.durableEntityOutboxEnabled = true
			hook := entityOutboxHook(t, e, app)
			request := entityRequest("confirmation")
			if kind == "environment" {
				request.Environment = "staging"
			}
			if kind == "tenant" {
				tenant, _, err := e.store.CreatePlatformTenant(t.Context(), e.acct.ID, "outbox-customer", "Outbox customer", 10)
				if err != nil {
					t.Fatal(err)
				}
				request.PlatformTenantID = tenant.ID
			}
			if kind == "foreign-destination" {
				other, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "other-app", RAMMB: 256})
				if err != nil {
					t.Fatal(err)
				}
				hook = entityOutboxHook(t, e, other)
			}
			work := seedAPIOutbox(t, e, app, request, hook.ID)
			switch kind {
			case "account":
				if err := e.store.UpdateAccountStatus(t.Context(), e.acct.ID, state.AccountSuspended); err != nil {
					t.Fatal(err)
				}
			case "abuse":
				if _, err := e.store.SetAccountAbuseHold(t.Context(), e.acct.ID, state.AccountAbuseHoldOperator, time.Now()); err != nil {
					t.Fatal(err)
				}
			case "plan":
				if err := e.store.UpdateAccountPlan(t.Context(), e.acct.ID, api.PlanFree); err != nil {
					t.Fatal(err)
				}
			case "app":
				if err := e.store.DeleteApp(t.Context(), app.ID); err != nil {
					t.Fatal(err)
				}
			case "allowlist":
				delete(e.s.durableEntityApps, app.ID)
			case "disabled":
				e.s.durableEntityOutboxEnabled = false
			case "tenant":
				if _, err := e.store.SetPlatformTenantStatus(t.Context(), e.acct.ID, request.PlatformTenantID, state.PlatformTenantSuspended); err != nil {
					t.Fatal(err)
				}
			case "destination":
				enabled := false
				if _, err := e.store.UpdateAppWebhook(t.Context(), hook.ID, state.UpdateAppWebhookParams{Enabled: &enabled}); err != nil {
					t.Fatal(err)
				}
			case "environment":
				dep, err := e.store.LiveDeploymentForScope(t.Context(), app.ID, "staging")
				if err != nil {
					t.Fatal(err)
				}
				if err := e.store.MarkDeploymentSuperseded(t.Context(), dep.ID); err != nil {
					t.Fatal(err)
				}
				if err := e.store.DeleteProjectEnvironment(t.Context(), e.acct.ID, app.ProjectID, "staging"); err != nil {
					t.Fatal(err)
				}
				if _, err := e.store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: e.acct.ID, ProjectID: app.ProjectID, Slug: "staging"}); err != nil {
					t.Fatal(err)
				}
			}
			if err := e.s.deliverDurableEntityOutbox(t.Context(), work); err == nil {
				t.Fatal("inadmissible work was accepted")
			}
			pending, err := e.s.durableEntities.PendingOutbox(t.Context(), work.Entity)
			if err != nil || len(pending.Messages) != 1 || pending.Version != 1 || dispatch.calls.Load() != 0 {
				t.Fatal(pending, err)
			}
			rows, _, err := e.store.ListAppWebhookDeliveries(t.Context(), hook.AppID, hook.ID, 100, "")
			if err != nil || len(rows) != 0 {
				t.Fatal("inadmissible delivery entered ledger", rows, err)
			}
		})
	}
}

func TestDurableEntityOutboxWorkerDisabledAndCancellation(t *testing.T) {
	e, _, _, _ := entityAPIFixture(t, false)
	e.s.runDurableEntityOutbox(t.Context())
	e.s.durableEntityOutboxEnabled = true
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	done := make(chan struct{})
	go func() { e.s.runDurableEntityOutbox(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker ignored cancellation")
	}
}
