// adr: 1297
package storedworkflowlive

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type suiteExecutionHTTPStore struct {
	calls              int
	app, name, id      string
	confirmed, runtime bool
	err                error
}

func (s *suiteExecutionHTTPStore) StartStoredWorkflowLiveSuite(ctx context.Context, app, name, id string, request api.StartStoredWorkflowLiveSuiteRequest, enabled bool) (api.StoredWorkflowLiveSuite, error) {
	if _, ok := ctx.Deadline(); !ok {
		return api.StoredWorkflowLiveSuite{}, errors.New("missing deadline")
	}
	s.calls++
	s.app, s.name, s.id, s.confirmed, s.runtime = app, name, id, request.ConfirmRealActions, enabled
	return api.StoredWorkflowLiveSuite{StoredWorkflowLiveSuiteSummary: api.StoredWorkflowLiveSuiteSummary{ID: id, Name: name, Managed: true}, Outcome: "pending"}, s.err
}
func (s *suiteExecutionHTTPStore) AdvanceStoredWorkflowLiveSuite(ctx context.Context, app, name, id string, request api.AdvanceStoredWorkflowLiveSuiteRequest, enabled bool) (api.StoredWorkflowLiveSuite, error) {
	return s.StartStoredWorkflowLiveSuite(ctx, app, name, id, api.StartStoredWorkflowLiveSuiteRequest{ConfirmRealActions: request.ConfirmRealActions}, enabled)
}
func TestManagedSuiteHTTPStrictWrites(t *testing.T) {
	for _, mode := range []string{"start", "advance", "unknown", "verdict", "trailing", "oversized", "backend-error"} {
		t.Run(mode, func(t *testing.T) {
			store := &suiteExecutionHTTPStore{}
			body, want := `{"confirm_real_actions":true}`, 200
			switch mode {
			case "unknown":
				body = `{"confirm_real_actions":true,"run_id":"PRIVATE"}`
				want = 400
			case "verdict":
				body = `{"confirm_real_actions":true,"outcome":"passed"}`
				want = 400
			case "trailing":
				body += `{}`
				want = 400
			case "oversized":
				body = `{"confirm_real_actions":` + strings.Repeat(" ", int(api.StoredWorkflowLiveSuiteRequestMaxBytes)) + `true}`
				want = 413
			case "backend-error":
				store.err = errors.New("PRIVATE-error")
				want = 503
			}
			r := httptest.NewRequest("POST", "/", strings.NewReader(body))
			r.SetPathValue("name", "report")
			r.SetPathValue("suite_id", "suite")
			w := httptest.NewRecorder()
			service := SuiteExecutionService{Store: store, RuntimeEnabled: true}
			if mode == "start" {
				service.Start(w, r, "app")
			} else {
				service.Advance(w, r, "app")
			}
			if w.Code != want || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "PRIVATE") {
				t.Fatalf("unsafe response: %d %s", w.Code, w.Body.String())
			}
			if want == 400 || want == 413 {
				if store.calls != 0 {
					t.Fatal("invalid body reached store")
				}
			} else if store.calls != 1 || store.app != "app" || store.name != "report" || store.id != "suite" || !store.confirmed || !store.runtime {
				t.Fatal("lost scope, confirmation or runtime state")
			}
		})
	}
}
