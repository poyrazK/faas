// adr: 1304
package storedworkflowconsole

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

func TestBaselineCatalogOnlyReadsAndRejectsForeignScope(t *testing.T) {
	for _, mode := range []string{"valid", "empty", "foreign", "duplicate", "error"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			receipt := api.StoredWorkflowLiveBaseline{AppID: "app", Name: "report", BaselineName: "staging", Revision: 1}
			view := api.ListStoredWorkflowLiveBaselinesResponse{ObservedAt: time.Now().UTC(), Baselines: []api.StoredWorkflowLiveBaselineHealth{{Baseline: receipt}}}
			switch mode {
			case "empty":
				view.Baselines = []api.StoredWorkflowLiveBaselineHealth{}
			case "foreign":
				view.Baselines[0].Baseline.AppID = "foreign"
			case "duplicate":
				view.Baselines = append(view.Baselines, view.Baselines[0])
			}
			host := &suiteInspectionHost{respond: func(op Operation) Response {
				calls++
				if op.Kind != "baseline-list" || op.Name != "report" {
					t.Fatal("catalog attempted another operation")
				}
				if mode == "error" {
					return Response{Status: 503, Body: []byte(`{"detail":"PRIVATE"}`)}
				}
				return inspectionJSON(t, view)
			}}
			service := Service{Host: host}
			data := dashboard.StoredWorkflowConsoleData{Name: "report"}
			service.inspectBaselineCatalog(httptest.NewRequest("GET", "/?action=promote-baseline&auto=true", nil), Scope{AppID: "app"}, &data)
			if calls != 1 || (data.BaselineCatalog != nil) != (mode == "valid" || mode == "empty") {
				t.Fatal("unsafe catalog")
			}
		})
	}
}
