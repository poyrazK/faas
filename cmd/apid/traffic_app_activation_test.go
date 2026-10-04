// adr: 531
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

type refusingTrafficAppStore struct {
	*state.MemStore
	refusal error
	calls   int
}

func (s *refusingTrafficAppStore) CreateAppIfUnderQuota(context.Context, state.App, api.Limits) (state.App, error) {
	s.calls++
	return state.App{}, s.refusal
}

func (s *refusingTrafficAppStore) CreateAppIfUnderQuotaWithActivity(context.Context, state.App, api.Limits, state.OrgActivity) (state.App, int64, error) {
	s.calls++
	return state.App{}, 0, s.refusal
}

func (s *refusingTrafficAppStore) RestoreApp(context.Context, string, api.Limits) (state.App, error) {
	s.calls++
	return state.App{}, s.refusal
}

func (s *refusingTrafficAppStore) RestoreAppWithActivity(context.Context, string, api.Limits, state.OrgActivity) (state.App, int64, error) {
	s.calls++
	return state.App{}, 0, s.refusal
}

func (s *refusingTrafficAppStore) UpdateApp(context.Context, string, state.UpdateAppParams) (state.App, error) {
	s.calls++
	return state.App{}, s.refusal
}

func (s *refusingTrafficAppStore) UpdateAppWithActivity(context.Context, string, state.UpdateAppParams, state.OrgActivity, state.OrgActivityAppConfigBuilder) (state.App, int64, error) {
	s.calls++
	return state.App{}, 0, s.refusal
}

func TestTrafficAppActivationHTTPRefusalRetainsIntentAndNotifications(t *testing.T) {
	for _, fixture := range []struct {
		name, code string
		refusal    error
		bytes      bool
	}{
		{"bytes", api.CodeTrafficPolicyTooLarge, &state.TrafficPolicyAggregateError{Scope: "host_rule_projection", Unit: "bytes", Limit: api.TrafficPolicyMaxHostBytes, Observed: api.TrafficPolicyMaxHostBytes + 1}, true},
		{"count", api.CodeTrafficPolicyTooLarge, &state.TrafficPolicyAggregateError{Scope: "host_rule_count", Unit: "rules", Limit: api.TrafficPolicyMaxHostRules, Observed: api.TrafficPolicyMaxHostRules + 1}, false},
		{"analysis", api.CodeTrafficPolicyTooComplex, &state.TrafficPolicyAnalysisError{Scope: "states", Unit: "states", Limit: api.TrafficPolicyMaxAnalysisStates, Observed: api.TrafficPolicyMaxAnalysisStates + 1}, false},
	} {
		for _, operation := range []string{"create", "restore", "publish"} {
			t.Run(fixture.name+"/"+operation, func(t *testing.T) {
				env, notifier := newTestServerWithCapturingNotifier(t, api.PlanPro)
				path := "/v1/apps"
				var body any = api.CreateAppRequest{Slug: "activation-guard", Runtime: "node22"}
				var original state.App
				method := http.MethodPost
				if operation == "restore" {
					original = seedApp(t, env, "activation-guard")
					var err error
					original, err = env.store.ScheduleAppDeletion(t.Context(), original.ID, time.Now().Add(time.Hour))
					if err != nil {
						t.Fatal(err)
					}
					path = "/v1/apps/" + original.Slug + "/restore"
					body = nil
				}
				if operation == "publish" {
					original = seedApp(t, env, "activation-guard")
					internal := api.AppVisibilityInternal
					var err error
					original, err = env.store.UpdateApp(t.Context(), original.ID, state.UpdateAppParams{SetVisibility: true, Visibility: &internal})
					if err != nil {
						t.Fatal(err)
					}
					method, path, body = http.MethodPatch, "/v1/apps/"+original.Slug, map[string]string{"visibility": "public"}
				}
				refusing := &refusingTrafficAppStore{MemStore: env.store, refusal: fmt.Errorf("activation: %w", fixture.refusal)}
				env.s.store = refusing
				response := env.do(t, method, path, body, nil)
				var problem api.Problem
				if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
					t.Fatal(err)
				}
				if response.Code != http.StatusUnprocessableEntity || problem.Code != fixture.code || problem.Limit == nil || problem.Observed == nil || *problem.Observed <= *problem.Limit || problem.DocsURL == "" ||
					fixture.bytes && (problem.LimitBytes == nil || problem.ObservedBytes == nil) || !fixture.bytes && (problem.LimitBytes != nil || problem.ObservedBytes != nil) || refusing.calls != 1 {
					t.Fatalf("activation refusal: calls=%d status=%d body=%s", refusing.calls, response.Code, response.Body.String())
				}
				apps, err := env.store.ListApps(t.Context(), env.acct.ID)
				expected := 0
				if operation == "publish" {
					expected = 1
				}
				if err != nil || len(apps) != expected {
					t.Fatalf("refused activation changed active apps: rows=%d err=%v", len(apps), err)
				}
				if operation == "restore" {
					saved, err := env.store.AppBySlugIncludingDeleted(t.Context(), original.Slug)
					if err != nil || saved.ID != original.ID || saved.Status != state.AppDeleted || saved.DeleteGraceUntil == nil || !saved.DeleteGraceUntil.Equal(*original.DeleteGraceUntil) {
						t.Fatalf("refused restore changed tombstone: %v", err)
					}
				}
				if operation == "publish" {
					saved, err := env.store.AppByID(t.Context(), original.ID)
					if err != nil || saved.Visibility != api.AppVisibilityInternal {
						t.Fatalf("refused publication changed visibility: %s err=%v", saved.Visibility, err)
					}
				}
				notifier.mu.Lock()
				defer notifier.mu.Unlock()
				for _, event := range notifier.emitted {
					if event.Channel == db.NotifyAppChanged {
						t.Fatalf("refused activation notified scheduler: %s", event.Payload)
					}
				}
			})
		}
	}
}
