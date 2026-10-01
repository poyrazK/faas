// adr: 375
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type refusingTrafficRevisionStore struct {
	*state.MemStore
	refusal error
	calls   int
}

func (s *refusingTrafficRevisionStore) CreateDeployment(ctx context.Context, deployment state.Deployment) (state.Deployment, error) {
	s.calls++
	if s.refusal != nil {
		return state.Deployment{}, s.refusal
	}
	return s.MemStore.CreateDeployment(ctx, deployment)
}

func (s *refusingTrafficRevisionStore) CreateDeploymentWithActivity(ctx context.Context, deployment state.Deployment, entry state.OrgActivity) (state.Deployment, int64, error) {
	s.calls++
	if s.refusal != nil {
		return state.Deployment{}, 0, s.refusal
	}
	return s.MemStore.CreateDeploymentWithActivity(ctx, deployment, entry)
}

func TestTrafficRevisionCreationHTTPRefusalPrivacyAndRepair(t *testing.T) {
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
			org := seedActivityPersonalOrg(t, e)
			app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, OrgID: org.ID, Slug: "traffic-revision-api", Status: state.AppActive})
			if err != nil {
				t.Fatal(err)
			}
			prior, err := e.store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage})
			if err != nil {
				t.Fatal(err)
			}
			before, err := e.store.ListDeploymentsForApp(t.Context(), app.ID, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			events, err := e.store.ListEvents(t.Context(), e.acct.ID, 0)
			if err != nil {
				t.Fatal(err)
			}
			refusing := &refusingTrafficRevisionStore{MemStore: e.store, refusal: &state.TrafficPolicyBindingError{Cause: fixture.cause}}
			e.s.store = refusing
			notifier.mu.Lock()
			emitted := len(notifier.emitted)
			notifier.mu.Unlock()
			body := api.CreateDeploymentRequest{Image: "registry.example.com/web@sha256:" + strings.Repeat("a", 64)}
			path := "/v1/apps/" + app.Slug + "/deployments"
			response := e.do(t, http.MethodPost, path, body, nil)
			text := response.Body.String()
			var problem api.Problem
			if response.Code != http.StatusUnprocessableEntity || json.Unmarshal(response.Body.Bytes(), &problem) != nil || problem.Code != fixture.code || problem.DocsURL == "" || problem.Limit == nil || problem.Observed == nil || *problem.Observed != *problem.Limit+1 {
				t.Fatalf("incomplete creation refusal contract: status=%d %s", response.Code, text)
			}
			if strings.Contains(text, "private.foreign") || strings.Contains(text, "private_foreign") || strings.Contains(text, "987") {
				t.Fatal("creation refusal disclosed foreign diagnostic evidence")
			}
			after, err := e.store.ListDeploymentsForApp(t.Context(), app.ID, 0, 0)
			if err != nil || !reflect.DeepEqual(before, after) || refusing.calls != 1 {
				t.Fatal("refused HTTP creation changed pending rows or retried")
			}
			afterEvents, err := e.store.ListEvents(t.Context(), e.acct.ID, 0)
			notifier.mu.Lock()
			changed := len(notifier.emitted) != emitted
			notifier.mu.Unlock()
			if err != nil || len(events) != len(afterEvents) || changed {
				t.Fatal("refused HTTP creation emitted audit or notification")
			}
			refusing.refusal = nil
			response = e.do(t, http.MethodPost, path, body, nil)
			if response.Code != http.StatusAccepted {
				t.Fatalf("creation after repair: status=%d %s", response.Code, response.Body.String())
			}
			current, err := e.store.DeploymentByID(t.Context(), prior.ID)
			if err != nil || current.Status != state.DeploySuperseded {
				t.Fatal("accepted HTTP creation did not supersede prior pending row")
			}
		})
	}
}
