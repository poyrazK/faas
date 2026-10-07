// adr: 678
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/onebox-faas/faas/pkg/state"
)

func (b *entityTestBucket) ListEntityPrefixes(ctx context.Context, prefix, cursor string, limit int32) (durableentity.EntityPrefixPage, error) {
	if err := ctx.Err(); err != nil {
		return durableentity.EntityPrefixPage{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	seen := map[string]bool{}
	for key := range b.objects {
		if strings.HasPrefix(key, prefix) {
			name, _, found := strings.Cut(strings.TrimPrefix(key, prefix), "/")
			if found && prefix+name+"/" > cursor {
				seen[prefix+name+"/"] = true
			}
		}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	page := durableentity.EntityPrefixPage{Prefixes: keys}
	if len(keys) > int(limit) {
		page.Prefixes = keys[:limit]
		page.NextCursor = page.Prefixes[len(page.Prefixes)-1]
	}
	return page, nil
}

func scheduleAPIAlarm(t *testing.T, e entityHTTPEnv, request api.DurableEntityInvokeRequest) durableentity.Alarm {
	t.Helper()
	at := time.Now().UTC().Add(-time.Second)
	request.Payload, _ = json.Marshal(struct {
		Delta int       `json:"delta"`
		At    time.Time `json:"alarm_at"`
	}{1, at})
	result := entityAPIResult(t, e.do(t, http.MethodPost, "/v1/apps/entity-counter/entities/invoke", request, nil))
	if result.Version != 1 {
		t.Fatalf("schedule version = %d", result.Version)
	}
	page, err := e.s.durableEntities.ScanDueAlarms(t.Context(), "")
	if err != nil || len(page.Alarms) != 1 || page.Failed != 0 {
		t.Fatalf("scheduled alarm discovery = %+v %v", page, err)
	}
	return page.Alarms[0]
}

func TestDurableEntityAlarmDispatchCommitReplayAndWorkerSweep(t *testing.T) {
	e, app, dispatch, _ := entityAPIFixture(t, true)
	e.s.durableEntityAlarmsEnabled = true
	request := entityRequest("schedule")
	request.Environment = "staging"
	alarm := scheduleAPIAlarm(t, e, request)
	if _, err := e.s.sweepDurableEntityAlarms(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	if err := e.s.deliverDurableEntityAlarm(t.Context(), alarm); err != nil {
		t.Fatal(err)
	}
	view, err := e.s.durableEntities.Read(t.Context(), alarm.Entity)
	if err != nil || view.AlarmAt != nil || view.Version != 2 || string(view.Data) != `{"count":2}` {
		t.Fatalf("alarm transition = %+v %v", view, err)
	}
	rows, err := e.store.ListInvocationsForApp(t.Context(), app.ID)
	if err != nil || len(rows) != 2 || dispatch.calls.Load() != 2 {
		t.Fatalf("alarm dispatched twice: rows=%d calls=%d err=%v", len(rows), dispatch.calls.Load(), err)
	}
	found := false
	for _, row := range rows {
		var envelope durableentity.HandlerRequest
		if err := json.Unmarshal(row.Payload, &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Event == "alarm" {
			found = true
			if row.EnvironmentID != alarm.Entity.EnvironmentID || row.DeploymentScope != "staging" || envelope.Entity != alarm.Entity || !durableentity.IsAlarmRequestID(envelope.RequestID) {
				t.Fatalf("alarm lost immutable scope: row=%+v envelope=%+v", row, envelope)
			}
		}
	}
	if !found {
		t.Fatal("guest did not receive alarm event")
	}
	request.RequestID = durableentity.AlarmRequest(alarm).ID
	if rec := e.do(t, http.MethodPost, "/v1/apps/entity-counter/entities/invoke", request, nil); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("forged alarm request accepted: %d", rec.Code)
	}
}

func TestDurableEntityAlarmRechecksCustomerAndRuntimeAdmission(t *testing.T) {
	for _, kind := range []string{"customer", "account", "app", "allowlist", "disabled", "environment"} {
		t.Run(kind, func(t *testing.T) {
			e, app, dispatch, _ := entityAPIFixture(t, kind == "environment")
			e.s.durableEntityAlarmsEnabled = true
			request := entityRequest("schedule")
			if kind == "environment" {
				request.Environment = "staging"
			}
			if kind == "customer" {
				tenant, _, err := e.store.CreatePlatformTenant(t.Context(), e.acct.ID, "alarm-customer", "Alarm customer", 10)
				if err != nil {
					t.Fatal(err)
				}
				request.PlatformTenantID = tenant.ID
			}
			alarm := scheduleAPIAlarm(t, e, request)
			switch kind {
			case "customer":
				if _, err := e.store.SetPlatformTenantStatus(t.Context(), e.acct.ID, request.PlatformTenantID, state.PlatformTenantSuspended); err != nil {
					t.Fatal(err)
				}
			case "account":
				if err := e.store.UpdateAccountStatus(t.Context(), e.acct.ID, state.AccountSuspended); err != nil {
					t.Fatal(err)
				}
			case "app":
				if err := e.store.DeleteApp(t.Context(), app.ID); err != nil {
					t.Fatal(err)
				}
			case "allowlist":
				delete(e.s.durableEntityApps, app.ID)
			case "disabled":
				e.s.durableEntityAlarmsEnabled = false
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
				dep, err = e.store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "staging", Kind: state.DeploymentKindImage})
				if err != nil {
					t.Fatal(err)
				}
				if err := e.store.MarkDeploymentLive(t.Context(), dep.ID); err != nil {
					t.Fatal(err)
				}
			}
			if err := e.s.deliverDurableEntityAlarm(t.Context(), alarm); err == nil {
				t.Fatal("held alarm was delivered")
			}
			if dispatch.calls.Load() != 1 {
				t.Fatal("inadmissible alarm reached guest")
			}
			view, err := e.s.durableEntities.Read(t.Context(), alarm.Entity)
			if err != nil || view.Version != 1 || view.AlarmAt == nil {
				t.Fatal("held alarm lost its deadline", view, err)
			}
		})
	}
}

func TestDurableEntityAlarmFailedGuestKeepsDeadline(t *testing.T) {
	e, _, dispatch, _ := entityAPIFixture(t, false)
	e.s.durableEntityAlarmsEnabled = true
	alarm := scheduleAPIAlarm(t, e, entityRequest("schedule"))
	dispatch.badBody.Store(true)
	if err := e.s.deliverDurableEntityAlarm(t.Context(), alarm); !errors.Is(err, durableentity.ErrInvalid) {
		t.Fatalf("invalid alarm guest response = %v", err)
	}
	view, err := e.s.durableEntities.Read(t.Context(), alarm.Entity)
	if err != nil || view.Version != 1 || view.AlarmAt == nil {
		t.Fatal("invalid guest consumed alarm", view, err)
	}
	dispatch.badBody.Store(false)
	if err := e.s.deliverDurableEntityAlarm(t.Context(), alarm); err != nil {
		t.Fatal(err)
	}
	view, err = e.s.durableEntities.Read(t.Context(), alarm.Entity)
	if err != nil || view.Version != 2 || view.AlarmAt != nil || string(view.Data) != `{"count":2}` {
		t.Fatal("alarm retry lost or duplicated state", view, err)
	}
}

func TestDurableEntityAlarmsRequirePreviewAndCancelWorker(t *testing.T) {
	e := entityAPISetup(t)
	if err := e.s.configureDurableEntities(t.Context(), func(key string) string {
		if key == "FAAS_DURABLE_ENTITY_ALARMS_ENABLED" {
			return "1"
		}
		return ""
	}); err == nil {
		t.Fatal("alarm gate accepted without entity preview")
	}
	e.s.runDurableEntityAlarms(t.Context()) // disabled returns immediately
	e, _, _, _ = entityAPIFixture(t, false)
	e.s.durableEntityAlarmsEnabled = true
	ctx, cancel := context.WithCancel(t.Context())
	finished := make(chan struct{})
	go func() { e.s.runDurableEntityAlarms(ctx); close(finished) }()
	cancel()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("alarm worker did not stop with its runtime context")
	}
}
