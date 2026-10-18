// adr: 1296
package automationlive

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type suiteHTTPStore struct {
	calls         []string
	app, name, id string
	request       api.SaveAutomationLiveSuiteRequest
	err           error
}

func (s *suiteHTTPStore) SaveAutomationLiveSuite(_ context.Context, app, name, id string, request api.SaveAutomationLiveSuiteRequest) (api.AutomationLiveSuite, error) {
	s.calls = append(s.calls, "save")
	s.app, s.name, s.id, s.request = app, name, id, request
	return api.AutomationLiveSuite{AutomationLiveSuiteSummary: api.AutomationLiveSuiteSummary{ID: id, Name: name}, Outcome: "incomplete"}, s.err
}
func (s *suiteHTTPStore) GetAutomationLiveSuite(_ context.Context, app, name, id string) (api.AutomationLiveSuite, error) {
	s.calls = append(s.calls, "get")
	s.app, s.name, s.id = app, name, id
	return api.AutomationLiveSuite{AutomationLiveSuiteSummary: api.AutomationLiveSuiteSummary{ID: id, Name: name}, Outcome: "pending"}, s.err
}
func (s *suiteHTTPStore) ListAutomationLiveSuites(_ context.Context, app, name string) (api.ListAutomationLiveSuitesResponse, error) {
	s.calls = append(s.calls, "list")
	s.app, s.name = app, name
	return api.ListAutomationLiveSuitesResponse{Suites: []api.AutomationLiveSuiteSummary{}}, s.err
}

func TestSuiteHTTPReadsAndStrictMetadataWrites(t *testing.T) {
	valid := `{"expected_version":7,"expected_published_version":4,"expected_deployment_id":"00000000-0000-4000-8000-000000000002","cases":[{"name":"first"}]}`
	for _, mode := range []string{"save", "get", "list", "unknown", "verdict", "payload", "trailing", "malformed", "oversized", "missing", "conflict"} {
		t.Run(mode, func(t *testing.T) {
			store := &suiteHTTPStore{}
			body, want := valid, 200
			switch mode {
			case "unknown":
				body = strings.Replace(valid, `"cases":`, `"unknown":true,"cases":`, 1)
				want = 400
			case "verdict":
				body = strings.Replace(valid, `"cases":`, `"outcome":"passed","cases":`, 1)
				want = 400
			case "payload":
				body = strings.Replace(valid, `"name":"first"`, `"name":"first","input":"PRIVATE"`, 1)
				want = 400
			case "trailing":
				body += `{}`
				want = 400
			case "malformed":
				body = `{"PRIVATE`
				want = 400
			case "oversized":
				body = `{"cases":[{"name":"` + strings.Repeat("x", int(api.AutomationLiveSuiteRequestMaxBytes)) + `"}]}`
				want = 413
			case "missing":
				store.err = state.ErrNotFound
				want = 404
			case "conflict":
				store.err = state.ErrWorkflowRunIdempotencyConflict
				want = 409
			}
			r := httptest.NewRequest("PUT", "/unused", strings.NewReader(body))
			r.SetPathValue("name", "report")
			r.SetPathValue("suite_id", "suite-id")
			w := httptest.NewRecorder()
			service := SuiteService{Store: store}
			switch mode {
			case "get", "missing":
				service.Get(w, r, "app-id")
			case "list":
				service.List(w, r, "app-id")
			default:
				service.Save(w, r, "app-id")
			}
			if w.Code != want || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "PRIVATE") {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if want == 400 || want == 413 {
				if len(store.calls) != 0 {
					t.Fatal("invalid metadata reached storage")
				}
			} else if len(store.calls) != 1 || store.app != "app-id" || store.name != "report" {
				t.Fatal("request escaped its supplied app/automation scope", store.calls)
			}
			if mode == "save" {
				var result api.AutomationLiveSuite
				if json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Outcome != "incomplete" || len(store.request.Cases) != 1 {
					t.Fatal("suite save lost typed metadata")
				}
			}
		})
	}
}
