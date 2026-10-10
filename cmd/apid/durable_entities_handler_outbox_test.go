// adr: 844
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/onebox-faas/faas/pkg/state"
)

func entityOutgoingGuest(t *testing.T, protocol int, intents []durableentity.OutboxIntent, beforeReturn func()) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var call durableentity.HandlerRequest
		if json.NewDecoder(r.Body).Decode(&call) != nil || call.ProtocolVersion != protocol {
			t.Fatal("unexpected guest protocol", call.ProtocolVersion)
		}
		if protocol == api.DurableEntityOutboxProtocolVersion && !reflect.DeepEqual(call.Limits, durableentity.OutboxHandlerLimits()) || protocol == api.DurableEntityProtocolVersion && call.Limits != nil {
			t.Fatal("protocol limits were absent or leaked into v1", call.Limits)
		}
		if beforeReturn != nil {
			beforeReturn()
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]int{"count": 1}, "result": map[string]int{"count": 1}, "outbox": intents})
	})
}

func guestIntent(hookID string) durableentity.OutboxIntent {
	return durableentity.OutboxIntent{WebhookID: hookID, EventType: "reservation.confirmed", Payload: json.RawMessage(`{"reservation":"123"}`)}
}

func TestDurableEntityGuestOutboxLostCommitAckReplayAndRelay(t *testing.T) {
	e, app, dispatch, bucket := entityAPIFixture(t, false)
	e.s.durableEntityOutboxEnabled, e.s.durableEntityOutboxHandlersEnabled = true, true
	hook := entityOutboxHook(t, e, app)
	dispatch.guest = entityOutgoingGuest(t, api.DurableEntityOutboxProtocolVersion, []durableentity.OutboxIntent{guestIntent(hook.ID)}, nil)
	request := entityRequest("reservation")
	id, _, problem := e.s.durableEntityIdentity((&http.Request{}).WithContext(t.Context()), e.acct, app, request)
	if problem != nil {
		t.Fatal(problem)
	}
	bucket.lose.Store(true)
	first := e.do(t, http.MethodPost, "/v1/apps/entity-counter/entities/invoke", request, nil)
	if first.Code != http.StatusServiceUnavailable || strings.Contains(first.Body.String(), "private-provider") {
		t.Fatal("uncertain publication was acknowledged or leaked errors", first.Code, first.Body.String())
	}
	pending, err := e.s.durableEntities.PendingOutbox(t.Context(), id)
	if err != nil || pending.Version != 1 || len(pending.Messages) != 1 {
		t.Fatal(pending, err)
	}
	restarted, err := durableentity.Open(t.Context(), bucket, durableentity.Options{})
	if err != nil {
		t.Fatal(err)
	}
	e.s.durableEntities, e.s.durableEntityOwner = restarted, uuid.NewString()
	replay := entityAPIResult(t, e.do(t, http.MethodPost, "/v1/apps/entity-counter/entities/invoke", request, nil))
	if !replay.Replayed || replay.Version != 1 || dispatch.calls.Load() != 1 {
		t.Fatal("receipt replay reran the guest", replay)
	}
	messageID := pending.Messages[0].ID
	if err := e.s.deliverDurableEntityOutbox(t.Context(), durableentity.OutboxWork{Entity: id, MessageID: messageID}); err != nil {
		t.Fatal(err)
	}
	rows, _, err := e.store.ListAppWebhookDeliveries(t.Context(), app.ID, hook.ID, 100, "")
	if err != nil || len(rows) != 1 || rows[0].ID != messageID || rows[0].Status != state.AppWebhookDeliveryPending {
		t.Fatal(rows, err)
	}
	status, err := restarted.InspectOutbox(t.Context(), id)
	if err != nil || status.Pending != 0 || status.Version != 1 {
		t.Fatal(status, err)
	}
	replay = entityAPIResult(t, e.do(t, http.MethodPost, "/v1/apps/entity-counter/entities/invoke", request, nil))
	if !replay.Replayed || dispatch.calls.Load() != 1 {
		t.Fatal("drained receipt replay appended new work", replay)
	}
}

func TestDurableEntityGuestOutboxGateKeepsV1Closed(t *testing.T) {
	for _, relay := range []bool{false, true} {
		for _, handlers := range []bool{false, true} {
			t.Run(fmt.Sprintf("relay=%t/handlers=%t", relay, handlers), func(t *testing.T) {
				e, _, dispatch, _ := entityAPIFixture(t, false)
				e.s.durableEntityOutboxEnabled, e.s.durableEntityOutboxHandlersEnabled = relay, handlers
				protocol := api.DurableEntityProtocolVersion
				want := http.StatusUnprocessableEntity
				if relay && handlers {
					protocol, want = api.DurableEntityOutboxProtocolVersion, http.StatusOK
				}
				dispatch.guest = entityOutgoingGuest(t, protocol, []durableentity.OutboxIntent{}, nil)
				rec := e.do(t, http.MethodPost, "/v1/apps/entity-counter/entities/invoke", entityRequest("empty-outbox"), nil)
				if rec.Code != want {
					t.Fatal("guest upgraded itself", rec.Code, rec.Body.String())
				}
			})
		}
	}
}

func TestDurableEntityGuestOutboxRechecksAdmissionBeforeStatePublication(t *testing.T) {
	for _, kind := range []string{"missing", "disabled-hook", "foreign-app", "foreign-account", "account-hook", "account", "app", "plan", "abuse", "allowlist", "handler-gate", "relay-gate", "tenant", "environment"} {
		t.Run(kind, func(t *testing.T) {
			e, app, dispatch, bucket := entityAPIFixture(t, kind == "environment")
			e.s.durableEntityOutboxEnabled, e.s.durableEntityOutboxHandlersEnabled = true, true
			hook := entityOutboxHook(t, e, app)
			request := entityRequest("reservation")
			if kind == "environment" {
				request.Environment = "staging"
			}
			if kind == "tenant" {
				tenant, _, err := e.store.CreatePlatformTenant(t.Context(), e.acct.ID, "guest-customer", "Guest customer", 10)
				if err != nil {
					t.Fatal(err)
				}
				request.PlatformTenantID = tenant.ID
			}
			id, _, problem := e.s.durableEntityIdentity((&http.Request{}).WithContext(t.Context()), e.acct, app, request)
			if problem != nil {
				t.Fatal(problem)
			}
			if kind == "foreign-app" || kind == "foreign-account" {
				owner := e.acct
				if kind == "foreign-account" {
					var err error
					owner, err = e.store.CreateAccount(t.Context(), "other-guest@example.test", api.PlanPro)
					if err != nil {
						t.Fatal(err)
					}
				}
				other, err := e.store.CreateApp(t.Context(), state.App{AccountID: owner.ID, Slug: "other-guest", RAMMB: 256})
				if err != nil {
					t.Fatal(err)
				}
				hook, err = e.store.CreateAppWebhook(t.Context(), state.AppWebhook{AccountID: owner.ID, AppID: other.ID, TargetURL: "https://other.example.test/", SecretSealed: []byte("sealed"), Enabled: true})
				if err != nil {
					t.Fatal(err)
				}
			}
			if kind == "account-hook" {
				var err error
				hook, err = e.store.CreateAccountReleaseWebhookIfUnderQuota(t.Context(), state.AppWebhook{AccountID: e.acct.ID, TargetURL: "https://account.example.test/", SecretSealed: []byte("sealed"), Enabled: true, EventFilter: []string{"deployment.live"}}, api.MustLimitsFor(e.acct.Plan))
				if err != nil {
					t.Fatal(err)
				}
			}
			beforeReturn := func() {
				var err error
				switch kind {
				case "missing":
					err = e.store.DeleteAppWebhook(t.Context(), hook.ID)
				case "disabled-hook":
					enabled := false
					_, err = e.store.UpdateAppWebhook(t.Context(), hook.ID, state.UpdateAppWebhookParams{Enabled: &enabled})
				case "account":
					err = e.store.UpdateAccountStatus(t.Context(), e.acct.ID, state.AccountSuspended)
				case "app":
					err = e.store.DeleteApp(t.Context(), app.ID)
				case "plan":
					err = e.store.UpdateAccountPlan(t.Context(), e.acct.ID, api.PlanFree)
				case "abuse":
					_, err = e.store.SetAccountAbuseHold(t.Context(), e.acct.ID, state.AccountAbuseHoldOperator, time.Now())
				case "allowlist":
					delete(e.s.durableEntityApps, app.ID)
				case "handler-gate":
					e.s.durableEntityOutboxHandlersEnabled = false
				case "relay-gate":
					e.s.durableEntityOutboxEnabled = false
				case "tenant":
					_, err = e.store.SetPlatformTenantStatus(t.Context(), e.acct.ID, request.PlatformTenantID, state.PlatformTenantSuspended)
				case "environment":
					// Current environment deletion fences admitted invocations.
					// Cancel the simulated guest before replacing its scope.
					if err = e.store.CancelInvocation(t.Context(), dispatch.currentInvocationID); err != nil {
						t.Fatal(err)
					}
					dep, loadErr := e.store.LiveDeploymentForScope(t.Context(), app.ID, "staging")
					if loadErr != nil {
						t.Fatal(loadErr)
					}
					if err = e.store.MarkDeploymentSuperseded(t.Context(), dep.ID); err == nil {
						err = e.store.DeleteProjectEnvironment(t.Context(), e.acct.ID, app.ProjectID, "staging")
					}
					if err == nil {
						_, err = e.store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: e.acct.ID, ProjectID: app.ProjectID, Slug: "staging"})
					}
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			dispatch.guest = entityOutgoingGuest(t, api.DurableEntityOutboxProtocolVersion, []durableentity.OutboxIntent{guestIntent(hook.ID)}, beforeReturn)
			rec := e.do(t, http.MethodPost, "/v1/apps/entity-counter/entities/invoke", request, nil)
			if rec.Code == http.StatusOK {
				t.Fatal("invalid outgoing destination/admission committed")
			}
			view, err := e.s.durableEntities.Read(t.Context(), id)
			if err != nil || view.Version != 0 || string(view.Data) != "{}" {
				t.Fatal("rejected outgoing work changed business state", view, err)
			}
			bucket.mu.Lock()
			defer bucket.mu.Unlock()
			for key := range bucket.objects {
				if strings.Contains(key, "/snapshots/") {
					t.Fatal("admission rejection uploaded a candidate snapshot")
				}
			}
		})
	}
}

func TestDurableEntityGuestOutboxFullQueuePreservesStateAndMessages(t *testing.T) {
	e, app, dispatch, _ := entityAPIFixture(t, false)
	e.s.durableEntityOutboxEnabled, e.s.durableEntityOutboxHandlersEnabled = true, true
	hook := entityOutboxHook(t, e, app)
	request := entityRequest("full")
	id, _, problem := e.s.durableEntityIdentity((&http.Request{}).WithContext(t.Context()), e.acct, app, request)
	if problem != nil {
		t.Fatal(problem)
	}
	for i := 0; i < api.MaxDurableEntityOutboxPending/api.MaxDurableEntityOutboxPerTransition; i++ {
		intents := make([]durableentity.OutboxIntent, api.MaxDurableEntityOutboxPerTransition)
		for j := range intents {
			intents[j] = guestIntent(hook.ID)
		}
		_, err := e.s.durableEntities.Invoke(t.Context(), id, uuid.NewString(), durableentity.Request{ID: fmt.Sprint(i), Payload: json.RawMessage(`null`)}, func(context.Context, durableentity.View) (durableentity.Transition, error) {
			return durableentity.Transition{Data: json.RawMessage(`{"count":0}`), Result: json.RawMessage(`null`), Outbox: intents}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	before, err := e.s.durableEntities.PendingOutbox(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	dispatch.guest = entityOutgoingGuest(t, api.DurableEntityOutboxProtocolVersion, []durableentity.OutboxIntent{guestIntent(hook.ID)}, nil)
	rec := e.do(t, http.MethodPost, "/v1/apps/entity-counter/entities/invoke", request, nil)
	if rec.Code != http.StatusConflict {
		t.Fatal(rec.Code, rec.Body.String())
	}
	after, err := e.s.durableEntities.PendingOutbox(t.Context(), id)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("full-queue rejection changed pending work", after, err)
	}
	view, err := e.s.durableEntities.Read(t.Context(), id)
	if err != nil || string(view.Data) != `{"count":0}` || view.Version != before.Version {
		t.Fatal(view, err)
	}
}

func TestDurableEntityAlarmGuestCanCommitOneOutgoingIntent(t *testing.T) {
	e, app, dispatch, _ := entityAPIFixture(t, false)
	e.s.durableEntityAlarmsEnabled = true
	alarm := scheduleAPIAlarm(t, e, entityRequest("schedule"))
	e.s.durableEntityOutboxEnabled, e.s.durableEntityOutboxHandlersEnabled = true, true
	hook := entityOutboxHook(t, e, app)
	dispatch.guest = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var call durableentity.HandlerRequest
		if json.NewDecoder(r.Body).Decode(&call) != nil || call.ProtocolVersion != api.DurableEntityOutboxProtocolVersion || call.Event != "alarm" || !reflect.DeepEqual(call.Limits, durableentity.OutboxHandlerLimits()) {
			t.Fatal("alarm did not negotiate v2", call)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]int{"count": 2}, "result": nil, "outbox": []durableentity.OutboxIntent{guestIntent(hook.ID)}})
	})
	for range 2 {
		if err := e.s.deliverDurableEntityAlarm(t.Context(), alarm); err != nil {
			t.Fatal(err)
		}
	}
	pending, err := e.s.durableEntities.PendingOutbox(t.Context(), alarm.Entity)
	if err != nil || pending.Version != 2 || len(pending.Messages) != 1 || pending.Messages[0].Version != 2 || dispatch.calls.Load() != 2 {
		t.Fatal("alarm replay duplicated outgoing work", pending, err)
	}
	view, err := e.s.durableEntities.Read(t.Context(), alarm.Entity)
	if err != nil || view.AlarmAt != nil || view.Version != 2 || string(view.Data) != `{"count":2}` {
		t.Fatal(view, err)
	}
}
