package appevents

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type overviewHealthStore struct {
	state.Store
	state.WorkflowRecoveryPolicyStore
	state.WorkflowRecoveryPolicyOperatorStore
	calls        int
	account, app string
}

func (s *overviewHealthStore) GetWorkflowRecoveryPolicyHealth(_ context.Context, account, app string, seconds int) (api.WorkflowRecoveryPolicyHealth, error) {
	s.calls++
	s.account = account
	s.app = app
	return api.WorkflowRecoveryPolicyHealth{Status: "inactive", StallAfterSeconds: seconds, Overview: &api.WorkflowRecoveryOverview{Status: "waiting", ManualAwaitingApproval: 1}}, nil
}

func (s *overviewHealthStore) GetWorkflowRecoveryAutomaticQueue(context.Context, string, string) (api.WorkflowRecoveryAutomaticQueue, error) {
	return api.WorkflowRecoveryAutomaticQueue{}, errors.New("health must use one coherent store snapshot")
}

type overviewHealthHost struct {
	Host
	store state.Store
}

func (h overviewHealthHost) Store() state.Store { return h.store }
func (h overviewHealthHost) LoadApp(_ http.ResponseWriter, _ *http.Request, _ state.Account, _ string) (state.App, bool) {
	return state.App{ID: "scoped-app"}, true
}

func TestRecoveryHealthOverviewHTTP(t *testing.T) {
	store := &overviewHealthStore{}
	s := Service{Host: overviewHealthHost{store: store}}
	r := httptest.NewRequest(http.MethodGet, "/v1/apps/orders/recovery-policy/health?stall_after_seconds=120", nil)
	r.SetPathValue("slug", "orders")
	w := httptest.NewRecorder()
	s.GetWorkflowRecoveryPolicyHealth(w, r, state.Account{ID: "scoped-account"})
	var out api.WorkflowRecoveryPolicyHealth
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" || out.Overview == nil || out.Overview.ManualAwaitingApproval != 1 || out.StallAfterSeconds != 120 || store.calls != 1 || store.account != "scoped-account" || store.app != "scoped-app" {
		t.Fatalf("response %d %+v calls %+v", w.Code, out, store)
	}
	for _, query := range []string{"stall_after_seconds=0", "stall_after_seconds=120&stall_after_seconds=300"} {
		w = httptest.NewRecorder()
		r = httptest.NewRequest(http.MethodGet, "/health?"+query, nil)
		s.GetWorkflowRecoveryPolicyHealth(w, r, state.Account{ID: "scoped-account"})
		if w.Code != http.StatusBadRequest || store.calls != 1 {
			t.Fatalf("invalid threshold %d calls %d", w.Code, store.calls)
		}
	}
}
