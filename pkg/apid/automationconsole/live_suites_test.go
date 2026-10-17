// adr: 1296
package automationconsole

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

type suiteInspectionHost struct{ respond func(Operation) Response }

func (*suiteInspectionHost) Scope(http.ResponseWriter, *http.Request) (Scope, bool) {
	return Scope{}, false
}
func (h *suiteInspectionHost) Request(_ *http.Request, _ Scope, op Operation) Response {
	return h.respond(op)
}
func (*suiteInspectionHost) IssueCSRF(http.ResponseWriter, Scope) string               { return "" }
func (*suiteInspectionHost) VerifyCSRF(http.ResponseWriter, *http.Request, Scope) bool { return false }

func TestSharedSuiteInspectionUsesOnlyScopedReads(t *testing.T) {
	for _, mode := range []string{"selected", "empty", "list-error", "get-error", "wrong-suite", "wrong-automation", "invalid-id"} {
		t.Run(mode, func(t *testing.T) {
			id := uuid.NewString()
			host := &suiteInspectionHost{respond: func(operation Operation) Response {
				if operation.Name != "report" {
					t.Fatal("suite read escaped selected automation")
				}
				switch operation.Kind {
				case "live-suite-list":
					if mode == "list-error" {
						return Response{Status: 503, Body: []byte(`{"detail":"PRIVATE"}`)}
					}
					raw, _ := json.Marshal(api.ListAutomationLiveSuitesResponse{Suites: []api.AutomationLiveSuiteSummary{{ID: id, Name: "report"}}})
					return Response{Status: 200, Body: raw}
				case "live-suite-get":
					if operation.RunID != id {
						t.Fatal("suite read changed selected identity")
					}
					if mode == "get-error" {
						return Response{Status: 404, Body: []byte(`{"detail":"PRIVATE"}`)}
					}
					view := api.AutomationLiveSuite{AutomationLiveSuiteSummary: api.AutomationLiveSuiteSummary{ID: id, Name: "report"}, Outcome: "pending"}
					if mode == "wrong-suite" {
						view.ID = uuid.NewString()
					}
					if mode == "wrong-automation" {
						view.Name = "other"
					}
					raw, _ := json.Marshal(view)
					return Response{Status: 200, Body: raw}
				default:
					t.Fatal("suite inspection attempted a mutation", operation.Kind)
					return Response{}
				}
			}}
			query := "?suite=" + id
			if mode == "invalid-id" {
				query = "?suite=invalid"
			}
			if mode == "empty" {
				query = ""
			}
			data := dashboard.AutomationConsoleData{Name: "report", Version: 7}
			service := Service{Host: host}
			service.inspectLiveSuites(httptest.NewRequest("GET", "/"+query, nil), Scope{AppID: "app", Slug: "billing"}, &data)
			if mode == "selected" && (data.LiveSuite == nil || data.LiveSuite.ID != id) {
				t.Fatal("selected suite not displayed")
			}
			if mode != "selected" && data.LiveSuite != nil {
				t.Fatal("unavailable suite appeared selected")
			}
			if strings.Contains(data.LiveSuiteError, "PRIVATE") {
				t.Fatal("raw backend error exposed")
			}
			if mode != "selected" && mode != "empty" && data.LiveSuiteError == "" {
				t.Fatal("failed inspection looked like empty history")
			}
		})
	}
}
