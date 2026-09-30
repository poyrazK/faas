// adr: 375
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type refusingTrafficAliasStore struct {
	*state.MemStore
	refusal error
	calls   int
}

func (s *refusingTrafficAliasStore) SetDeploymentAlias(context.Context, string, string, string) (state.DeploymentAlias, error) {
	s.calls++
	return state.DeploymentAlias{}, fmt.Errorf("alias publication: %w", s.refusal)
}

func TestTrafficAliasActivationHTTPRefusalRetainsIntent(t *testing.T) {
	for _, fixture := range []struct {
		name, code string
		refusal    error
	}{
		{"bytes", api.CodeTrafficPolicyTooLarge, &state.TrafficPolicyAggregateError{Scope: "host_rule_projection", Unit: "bytes", Limit: api.TrafficPolicyMaxHostBytes, Observed: api.TrafficPolicyMaxHostBytes + 1}},
		{"count", api.CodeTrafficPolicyTooLarge, &state.TrafficPolicyAggregateError{Scope: "host_rule_count", Unit: "rules", Limit: api.TrafficPolicyMaxHostRules, Observed: api.TrafficPolicyMaxHostRules + 1}},
		{"analysis", api.CodeTrafficPolicyTooComplex, &state.TrafficPolicyAnalysisError{Scope: "states", Unit: "states", Limit: api.TrafficPolicyMaxAnalysisStates, Observed: api.TrafficPolicyMaxAnalysisStates + 1}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			e, notifier := newTestServerWithCapturingNotifier(t, api.PlanPro)
			deployment := mustSeedDeployment(t, e, "alias-activation")
			refusing := &refusingTrafficAliasStore{MemStore: e.store, refusal: fixture.refusal}
			e.s.store = refusing
			notifier.mu.Lock()
			notificationsBefore := len(notifier.emitted)
			notifier.mu.Unlock()
			response := e.do(t, http.MethodPut, "/v1/apps/alias-activation/deployment-aliases/candidate", api.SetDeploymentAliasRequest{DeploymentID: deployment.ID}, nil)
			if response.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body["code"] != fixture.code || body["limit"] == nil || body["observed"] == nil || body["docs_url"] == nil {
				t.Fatalf("missing stable traffic write evidence: %+v", body)
			}
			if refusing.calls != 1 {
				t.Fatalf("store calls=%d want=1", refusing.calls)
			}
			rows, err := e.store.ListDeploymentAliases(t.Context(), deployment.AppID)
			if err != nil || len(rows) != 0 {
				t.Fatalf("refused alias retained intent: %+v err=%v", rows, err)
			}
			notifier.mu.Lock()
			defer notifier.mu.Unlock()
			if len(notifier.emitted) != notificationsBefore {
				t.Fatal("refused alias emitted a notification")
			}
		})
	}
}
