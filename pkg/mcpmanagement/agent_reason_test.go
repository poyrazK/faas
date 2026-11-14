package mcpmanagement

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// Applying a reviewed plan sends the plan's reason as Gregale-Agent-Reason
// on its write requests (ADR-20261110182236); other calls send none.
func TestApplySendsThePlanReason(t *testing.T) {
	f := &operationFixture{base: newLocalFixture(t, true, "release-base"), revision: 1, values: map[string]string{}}
	var mu sync.Mutex
	writes := []string{}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			mu.Lock()
			writes = append(writes, r.Header.Get(api.AgentReasonHeader))
			mu.Unlock()
		}
		f.serveHTTP(w, r)
	}))
	t.Cleanup(backend.Close)
	_, m, err := newManagementServer(api.NewClient(backend.URL, testToken), Options{APIBase: backend.URL, Token: testToken, AllowWrites: true, agentKey: true, Capabilities: []string{api.ScopeEnvWrite}})
	if err != nil {
		t.Fatal(err)
	}
	ram := 512
	plan, err := m.planScale(t.Context(), scalePlanArgs{App: "alpha", Reason: "more RAM for the\nimport job", RAMMB: &ram})
	if err != nil {
		t.Fatal(err)
	}
	id := plan.(releasePlan).ID
	if got := api.AgentReasonFromContext(m.withPlanReason(context.Background(), applyArgs{PlanID: "unknown"})); got != "" {
		t.Fatalf("unknown plan reason = %q", got)
	}
	if got := api.AgentReasonFromContext(m.withPlanReason(context.Background(), scalePlanArgs{App: "alpha"})); got != "" {
		t.Fatalf("a planning call got reason %q", got)
	}
	mu.Lock()
	writes = writes[:0]
	mu.Unlock()
	ctx := m.withPlanReason(t.Context(), applyArgs{PlanID: id})
	if _, err := m.applyOperation(ctx, applyArgs{PlanID: id}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(writes) == 0 {
		t.Fatal("apply made no write request")
	}
	for _, got := range writes {
		if got != "more RAM for the import job" {
			t.Fatalf("write reasons = %q; want the plan's sanitised reason on each", writes)
		}
	}
}
