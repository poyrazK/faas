// adr: 846
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/onebox-faas/faas/pkg/state"
)

func apiRecoveryFixture(t *testing.T, target string) (entityHTTPEnv, state.App, *entityDispatchFixture, api.DurableEntityRetryRequest) {
	t.Helper()
	e, app, dispatch, bucket := entityAPIFixture(t, false)
	now := time.Now().UTC()
	engine, err := durableentity.Open(t.Context(), bucket, durableentity.Options{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	e.s.durableEntities = engine
	selectors := entityRequest("recovery")
	id, _, problem := e.s.durableEntityIdentity((&http.Request{}).WithContext(t.Context()), e.acct, app, selectors)
	if problem != nil {
		t.Fatal(problem)
	}
	if target == "outbox" {
		hook := entityOutboxHook(t, e, app)
		work := seedAPIOutbox(t, e, app, selectors, hook.ID)
		for range api.MaxDurableEntityOutboxAttempts {
			claim, err := engine.Acquire(t.Context(), id, "recovery-fixture")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := engine.ReserveOutbox(t.Context(), claim, work.MessageID); err != nil {
				t.Fatal(err)
			}
			if err := engine.Release(t.Context(), claim); err != nil {
				t.Fatal(err)
			}
			status, err := engine.Inspect(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			now = *status.Outbox.NextAttemptAt
		}
	} else {
		at := now
		if _, err := engine.Invoke(t.Context(), id, "recovery-fixture", durableentity.Request{ID: selectors.RequestID, Payload: selectors.Payload}, func(context.Context, durableentity.View) (durableentity.Transition, error) {
			return durableentity.Transition{Data: json.RawMessage(`{"reserved":true}`), Result: json.RawMessage(`{}`), AlarmAt: &at}, nil
		}); err != nil {
			t.Fatal(err)
		}
		for range api.MaxDurableEntityAlarmAttempts {
			if _, err := engine.InvokeAlarm(t.Context(), durableentity.Alarm{Entity: id, Version: 1, At: at}, "recovery-fixture", func(context.Context, durableentity.View) (durableentity.Transition, error) {
				return durableentity.Transition{}, errors.New("failed alarm")
			}); err == nil {
				t.Fatal("failed alarm succeeded")
			}
			status, err := engine.Inspect(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			now = *status.Alarm.NextAttemptAt
		}
	}
	observed, err := engine.Inspect(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	request := api.DurableEntityRetryRequest{Namespace: selectors.Namespace, Key: selectors.Key, Target: target, ExpectedVersion: observed.Version, ExpectedRecoveryRevision: observed.RecoveryRevision}
	if target == "outbox" {
		request.HeadID = observed.Outbox.HeadID
	} else {
		request.AlarmAt = &observed.Alarm.Alarm.At
	}
	return e, app, dispatch, request
}

func TestEntityRecoveryOwnerMutationWithoutGuestExecution(t *testing.T) {
	for _, target := range []string{"alarm", "outbox"} {
		t.Run(target, func(t *testing.T) {
			e, app, dispatch, request := apiRecoveryFixture(t, target)
			path := "/v1/apps/" + app.Slug + "/entities/retry"
			rec := e.do(t, http.MethodPost, path, request, nil)
			var out api.DurableEntityRetryResponse
			if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil || !out.Rearmed || out.Version != request.ExpectedVersion || out.Target != target || rec.Header().Get("Cache-Control") != "private, no-store" || dispatch.calls.Load() != 0 {
				t.Fatal(rec.Code, rec.Body.String())
			}
			again := e.do(t, http.MethodPost, path, request, nil)
			if again.Code != http.StatusConflict {
				t.Fatal("recovery replay was not fenced", again.Code, again.Body.String())
			}
			events, err := e.store.ListEvents(t.Context(), e.acct.ID, 100)
			var logs []state.Event
			for _, event := range events {
				if event.Kind == "durable_entity.retry_rearmed" {
					logs = append(logs, event)
				}
			}
			if err != nil || len(logs) != 1 || !strings.Contains(string(logs[0].Data), request.ExpectedRecoveryRevision) {
				t.Fatal("recovery audit missing", logs, err)
			}
			for _, forbidden := range []string{`"payload"`, `"data"`, `"claim_token"`, `"snapshot_key"`, `"key"`} {
				if strings.Contains(string(logs[0].Data), forbidden) {
					t.Fatal("audit exposed private state", forbidden)
				}
			}
		})
	}
}

func TestEntityRecoveryRejectsReadScopeAndMalformedTargets(t *testing.T) {
	e, app, _, request := apiRecoveryFixture(t, "outbox")
	path := "/v1/apps/" + app.Slug + "/entities/retry"
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "reader", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	denied := e.do(t, http.MethodPost, path, request, map[string]string{"Authorization": "Bearer " + key})
	if denied.Code != http.StatusForbidden {
		t.Fatal(denied.Code, denied.Body.String())
	}
	for _, kind := range []string{"both-targets", "no-revision", "zero-version", "unknown-target"} {
		malformed := request
		switch kind {
		case "both-targets":
			now := time.Now()
			malformed.AlarmAt = &now
		case "no-revision":
			malformed.ExpectedRecoveryRevision = ""
		case "zero-version":
			malformed.ExpectedVersion = 0
		case "unknown-target":
			malformed.Target = "delivery"
		}
		rec := e.do(t, http.MethodPost, path, malformed, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatal(kind, rec.Code, rec.Body.String())
		}
	}
	missing := request
	missing.Key = "missing"
	rec := e.do(t, http.MethodPost, path, missing, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatal(rec.Code, rec.Body.String())
	}
	stale := request
	stale.ExpectedVersion++
	rec = e.do(t, http.MethodPost, path, stale, nil)
	if rec.Code != http.StatusConflict {
		t.Fatal(rec.Code, rec.Body.String())
	}
}

func TestEntityRecoveryRechecksMutationAdmission(t *testing.T) {
	for _, kind := range []string{"plan", "abuse", "app-disabled", "suspended-customer"} {
		t.Run(kind, func(t *testing.T) {
			e, app, dispatch, request := apiRecoveryFixture(t, "outbox")
			want := http.StatusForbidden
			switch kind {
			case "plan":
				want = http.StatusPaymentRequired
				if err := e.store.UpdateAccountPlan(t.Context(), e.acct.ID, api.PlanFree); err != nil {
					t.Fatal(err)
				}
			case "abuse":
				want = http.StatusPaymentRequired
				if _, err := e.store.SetAccountAbuseHold(t.Context(), e.acct.ID, state.AccountAbuseHoldOperator, time.Now()); err != nil {
					t.Fatal(err)
				}
			case "app-disabled":
				delete(e.s.durableEntityApps, app.ID)
				want = http.StatusServiceUnavailable
			case "suspended-customer":
				tenant, _, err := e.store.CreatePlatformTenant(t.Context(), e.acct.ID, "recover-customer", "Recovery customer", 10)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := e.store.SetPlatformTenantStatus(t.Context(), e.acct.ID, tenant.ID, state.PlatformTenantSuspended); err != nil {
					t.Fatal(err)
				}
				request.PlatformTenantID = tenant.ID
			}
			rec := e.do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/entities/retry", request, nil)
			if rec.Code != want || dispatch.calls.Load() != 0 {
				t.Fatal(kind, rec.Code, rec.Body.String())
			}
		})
	}
}
