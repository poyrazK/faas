// adr: 590
package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAsyncRouteEnvironmentComesFromResolvedHost(t *testing.T) {
	enqueuer := &recordingAsyncRouteEnqueuer{}
	h := &Handler{asyncRoutes: enqueuer}
	request := httptest.NewRequest(http.MethodPost, "https://stage.example.test/work", strings.NewReader(`{}`))
	request.Header.Set("X-Gregale-Environment", "production")
	app := App{ID: "app", AccountID: "account", ProjectID: "project", Scope: "staging", Plan: api.PlanPro, RequestInvocationsEnabled: true}
	response := httptest.NewRecorder()
	if !h.applyEdgeRuleAsync(response, request, app, &EdgeRuleAsyncResolved{ID: "rule"}) || response.Code != http.StatusAccepted {
		t.Fatalf("stage async = %d %s", response.Code, response.Body.String())
	}
	if enqueuer.request.Scope != "staging" {
		t.Fatalf("untrusted header changed admitted scope: %q", enqueuer.request.Scope)
	}
}

func TestAsyncRouteEnvironmentWorkIsolationHasNamedFailure(t *testing.T) {
	enqueuer := &recordingAsyncRouteEnqueuer{err: fmt.Errorf("admit stage: %w", state.ErrInvocationEnvironmentWorkIsolation)}
	h := &Handler{asyncRoutes: enqueuer}
	request := httptest.NewRequest(http.MethodPost, "https://stage.example.test/work", strings.NewReader(`{}`))
	app := App{ID: "app", AccountID: "account", ProjectID: "project", Scope: "staging", Plan: api.PlanPro, RequestInvocationsEnabled: true}
	response := httptest.NewRecorder()
	h.applyEdgeRuleAsync(response, request, app, &EdgeRuleAsyncResolved{ID: "rule"})
	var problem api.Problem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil || response.Code != http.StatusConflict || problem.Code != "invocation_environment_work_isolation_unavailable" {
		t.Fatalf("stage isolation failure = %d %s, %v", response.Code, response.Body.String(), err)
	}
}
