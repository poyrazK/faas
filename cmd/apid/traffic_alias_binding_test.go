// adr: 570
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

type refusingTrafficAliasRemovalStore struct {
	*state.MemStore
	refusal error
	calls   int
}

func (s *refusingTrafficAliasRemovalStore) DeleteDeploymentAlias(ctx context.Context, app, name string) error {
	s.calls++
	if s.refusal != nil {
		return s.refusal
	}
	return s.MemStore.DeleteDeploymentAlias(ctx, app, name)
}

func TestTrafficAliasBindingHTTPRefusalPrivacyAndRepair(t *testing.T) {
	for _, fixture := range []struct {
		name, code string
		cause      error
	}{
		{"aggregate", api.CodeTrafficPolicyTooLarge, &state.TrafficPolicyAggregateError{Scope: "private_foreign_scope", Host: "private.foreign.example", Unit: "bytes", Limit: api.TrafficPolicyMaxHostBytes, Observed: 2*api.TrafficPolicyMaxHostBytes + 987}},
		{"projection", api.CodeTrafficPolicyTooLarge, &state.TrafficPolicyProjectionError{Scope: "private_foreign_scope", Limit: api.TrafficPolicyMaxContractBytes, Observed: 2*api.TrafficPolicyMaxContractBytes + 987}},
		{"analysis", api.CodeTrafficPolicyTooComplex, &state.TrafficPolicyAnalysisError{Scope: "private_foreign_scope", Unit: "states", Limit: api.TrafficPolicyMaxAnalysisStates, Observed: 2*api.TrafficPolicyMaxAnalysisStates + 987}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			e := setup(t, api.PlanPro)
			deployment := mustSeedDeployment(t, e, "traffic-alias-api")
			app, err := e.store.AppBySlug(t.Context(), "traffic-alias-api")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.store.SetDeploymentAlias(t.Context(), app.ID, "x", deployment.ID); err != nil {
				t.Fatal(err)
			}
			before, err := e.store.ListDeploymentAliases(t.Context(), app.ID)
			if err != nil {
				t.Fatal(err)
			}
			events, err := e.store.ListEvents(t.Context(), e.acct.ID, 0)
			if err != nil {
				t.Fatal(err)
			}
			refusing := &refusingTrafficAliasRemovalStore{MemStore: e.store, refusal: &state.TrafficPolicyBindingError{Cause: fixture.cause}}
			e.s.store = refusing
			path := "/v1/apps/traffic-alias-api/deployment-aliases/x"
			response := e.do(t, http.MethodDelete, path, nil, nil)
			text := response.Body.String()
			if response.Code != http.StatusUnprocessableEntity || strings.Contains(text, "private.foreign") || strings.Contains(text, "private_foreign") || strings.Contains(text, "987") {
				t.Fatalf("alias refusal exposed foreign evidence: status=%d body=%s", response.Code, text)
			}
			var problem api.Problem
			if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil || problem.Code != fixture.code || problem.DocsURL == "" || problem.Limit == nil || problem.Observed == nil || *problem.Observed != *problem.Limit+1 {
				t.Fatalf("alias refusal lacks stable problem contract: %s", text)
			}
			after, err := e.store.ListDeploymentAliases(t.Context(), app.ID)
			if err != nil || !reflect.DeepEqual(before, after) || refusing.calls != 1 {
				t.Fatal("refused alias deletion changed mapping or retried")
			}
			afterEvents, err := e.store.ListEvents(t.Context(), e.acct.ID, 0)
			if err != nil || len(events) != len(afterEvents) {
				t.Fatal("refused alias deletion emitted activity")
			}
			refusing.refusal = nil
			response = e.do(t, http.MethodDelete, path, nil, nil)
			if response.Code != http.StatusNoContent {
				t.Fatalf("alias deletion after repair: status=%d body=%s", response.Code, response.Body.String())
			}
			after, err = e.store.ListDeploymentAliases(t.Context(), app.ID)
			if err != nil || len(after) != 0 {
				t.Fatal("accepted alias deletion retained its mapping")
			}
		})
	}
}
