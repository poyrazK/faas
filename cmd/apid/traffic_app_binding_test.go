// adr: 375
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type refusingTrafficAppBindingStore struct {
	*state.MemStore
	refusal error
	calls   int
}

func (s *refusingTrafficAppBindingStore) UpdateAppWithActivity(ctx context.Context, id string, p state.UpdateAppParams, entry state.OrgActivity, build state.OrgActivityAppConfigBuilder) (state.App, int64, error) {
	s.calls++
	if s.refusal != nil {
		return state.App{}, 0, s.refusal
	}
	return s.MemStore.UpdateAppWithActivity(ctx, id, p, entry, build)
}
func (s *refusingTrafficAppBindingStore) UpdateApp(ctx context.Context, id string, p state.UpdateAppParams) (state.App, error) {
	s.calls++
	if s.refusal != nil {
		return state.App{}, s.refusal
	}
	return s.MemStore.UpdateApp(ctx, id, p)
}
func (s *refusingTrafficAppBindingStore) ScheduleAppDeletionWithActivity(ctx context.Context, id string, grace time.Time, entry state.OrgActivity) (state.App, int64, error) {
	s.calls++
	if s.refusal != nil {
		return state.App{}, 0, s.refusal
	}
	return s.MemStore.ScheduleAppDeletionWithActivity(ctx, id, grace, entry)
}
func (s *refusingTrafficAppBindingStore) ScheduleAppDeletion(ctx context.Context, id string, grace time.Time) (state.App, error) {
	s.calls++
	if s.refusal != nil {
		return state.App{}, s.refusal
	}
	return s.MemStore.ScheduleAppDeletion(ctx, id, grace)
}
func (s *refusingTrafficAppBindingStore) RestoreAppWithActivity(ctx context.Context, id string, limits api.Limits, entry state.OrgActivity) (state.App, int64, error) {
	s.calls++
	if s.refusal != nil {
		return state.App{}, 0, s.refusal
	}
	return s.MemStore.RestoreAppWithActivity(ctx, id, limits, entry)
}
func (s *refusingTrafficAppBindingStore) RestoreApp(ctx context.Context, id string, limits api.Limits) (state.App, error) {
	s.calls++
	if s.refusal != nil {
		return state.App{}, s.refusal
	}
	return s.MemStore.RestoreApp(ctx, id, limits)
}
func (s *refusingTrafficAppBindingStore) RenameApp(ctx context.Context, account, old, next string) (state.App, error) {
	s.calls++
	if s.refusal != nil {
		return state.App{}, s.refusal
	}
	return s.MemStore.RenameApp(ctx, account, old, next)
}

func TestTrafficAppBindingHTTPRefusalPrivacyAndRepair(t *testing.T) {
	for _, mode := range []string{"visibility", "delete", "restore", "rename"} {
		t.Run(mode, func(t *testing.T) {
			for _, fixture := range []struct {
				name, code string
				cause      error
			}{
				{"aggregate", api.CodeTrafficPolicyTooLarge, &state.TrafficPolicyAggregateError{Scope: "private_foreign_scope", Host: "private.foreign.example", Unit: "bytes", Limit: api.TrafficPolicyMaxHostBytes, Observed: 2*api.TrafficPolicyMaxHostBytes + 987}},
				{"projection", api.CodeTrafficPolicyTooLarge, &state.TrafficPolicyProjectionError{Scope: "private_foreign_scope", Limit: api.TrafficPolicyMaxContractBytes, Observed: 2*api.TrafficPolicyMaxContractBytes + 987}},
				{"analysis", api.CodeTrafficPolicyTooComplex, &state.TrafficPolicyAnalysisError{Scope: "private_foreign_scope", Unit: "states", Limit: api.TrafficPolicyMaxAnalysisStates, Observed: 2*api.TrafficPolicyMaxAnalysisStates + 987}},
			} {
				t.Run(fixture.name, func(t *testing.T) {
					e, notifier := newTestServerWithCapturingNotifier(t, api.PlanPro)
					app := seedApp(t, e, "traffic-app-binding-api")
					if mode == "restore" {
						var err error
						app, err = e.store.ScheduleAppDeletion(t.Context(), app.ID, time.Now().Add(time.Hour))
						if err != nil {
							t.Fatal(err)
						}
					}
					inv, err := e.store.EnqueueInvocation(t.Context(), state.Invocation{AppID: app.ID, AccountID: e.acct.ID, Source: state.InvocationDelayedTask, Payload: json.RawMessage(`{}`), DueAt: time.Now().Add(time.Hour)})
					if err != nil {
						t.Fatal(err)
					}
					refusing := &refusingTrafficAppBindingStore{MemStore: e.store, refusal: &state.TrafficPolicyBindingError{Cause: fixture.cause}}
					e.s.store = refusing
					method, path, body := http.MethodPatch, "/v1/apps/"+app.Slug, any(map[string]any{"visibility": "internal"})
					want := http.StatusOK
					switch mode {
					case "delete":
						method, body, want = http.MethodDelete, nil, http.StatusNoContent
					case "restore":
						method, path, body = http.MethodPost, path+"/restore", nil
					case "rename":
						method, path, body = http.MethodPost, path+"/rename", map[string]any{"new_slug": "traffic-app-binding-renamed"}
					}
					before, err := e.store.AppByID(t.Context(), app.ID)
					if err != nil {
						t.Fatal(err)
					}
					events, err := e.store.ListEvents(t.Context(), e.acct.ID, 0)
					if err != nil {
						t.Fatal(err)
					}
					notifier.mu.Lock()
					emitted := len(notifier.emitted)
					notifier.mu.Unlock()
					response := e.do(t, method, path, body, nil)
					if response.Code != http.StatusUnprocessableEntity {
						t.Fatalf("refusal status=%d body=%s", response.Code, response.Body.String())
					}
					text := response.Body.String()
					if strings.Contains(text, "private.foreign") || strings.Contains(text, "private_foreign") || strings.Contains(text, "987") {
						t.Fatal("app refusal disclosed foreign evidence")
					}
					var problem api.Problem
					if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil || problem.Code != fixture.code || problem.DocsURL == "" || problem.Limit == nil || problem.Observed == nil || *problem.Observed != *problem.Limit+1 {
						t.Fatalf("incomplete binding problem: %s", text)
					}
					current, err := e.store.AppByID(t.Context(), app.ID)
					if err != nil || !reflect.DeepEqual(before, current) {
						t.Fatal("refusal changed app")
					}
					currentInv, err := e.store.InvocationByID(t.Context(), inv.ID)
					if err != nil || !reflect.DeepEqual(inv, currentInv) {
						t.Fatal("refused HTTP deletion canceled invocation")
					}
					afterEvents, err := e.store.ListEvents(t.Context(), e.acct.ID, 0)
					notifier.mu.Lock()
					changed := len(notifier.emitted) != emitted
					notifier.mu.Unlock()
					if err != nil || len(events) != len(afterEvents) || changed || refusing.calls != 1 {
						t.Fatal("refusal emitted audit/notification or retried mutation")
					}
					refusing.refusal = nil
					response = e.do(t, method, path, body, nil)
					if response.Code != want {
						t.Fatalf("repair status=%d body=%s", response.Code, response.Body.String())
					}
					if mode == "delete" {
						currentInv, err = e.store.InvocationByID(t.Context(), inv.ID)
						if err != nil || currentInv.State != state.InvocationCancelled {
							t.Fatal("accepted deletion did not cancel invocation")
						}
					}
				})
			}
		})
	}
}

func TestTrafficAppBindingErrorRetainsTypedCause(t *testing.T) {
	cause := &state.TrafficPolicyAggregateError{Scope: "private", Host: "private", Limit: 1, Observed: 2}
	var aggregate *state.TrafficPolicyAggregateError
	if !errors.As(&state.TrafficPolicyBindingError{Cause: cause}, &aggregate) || aggregate != cause {
		t.Fatal("binding error lost typed diagnostic")
	}
}
