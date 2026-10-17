// adr: 1298
package automationconsole

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

func TestLiveSuiteComparisonInspectionIsReadOnlyAndValidatesPair(t *testing.T) {
	for _, mode := range []string{"valid", "invalid-baseline", "wrong-baseline", "wrong-candidate", "wrong-name", "error"} {
		t.Run(mode, func(t *testing.T) {
			candidate, baseline := uuid.NewString(), uuid.NewString()
			calls := 0
			host := &suiteInspectionHost{respond: func(op Operation) Response {
				if op.Kind != "live-suite-compare" || op.Name != "report" || op.RunID != candidate || op.BaselineID != baseline {
					t.Fatal("comparison attempted another operation or scope", op)
				}
				calls++
				view := api.AutomationLiveSuiteComparison{Baseline: api.AutomationLiveSuiteSummary{ID: baseline, Name: "report"}, Candidate: api.AutomationLiveSuiteSummary{ID: candidate, Name: "report"}}
				switch mode {
				case "wrong-baseline":
					view.Baseline.ID = uuid.NewString()
				case "wrong-candidate":
					view.Candidate.ID = uuid.NewString()
				case "wrong-name":
					view.Baseline.Name = "PRIVATE-foreign"
				case "error":
					return Response{Status: 404, Body: []byte(`{"detail":"PRIVATE"}`)}
				}
				raw, _ := json.Marshal(view)
				return Response{Status: 200, Body: raw}
			}}
			query := baseline
			if mode == "invalid-baseline" {
				query = "invalid"
			}
			data := dashboard.AutomationConsoleData{Name: "report", LiveSuiteID: candidate}
			service := Service{Host: host}
			service.inspectLiveSuiteComparison(httptest.NewRequest("GET", "/?baseline="+query+"&auto=true", nil), Scope{}, &data)
			if (data.LiveSuiteComparison != nil) != (mode == "valid") || strings.Contains(data.LiveSuiteComparisonError, "PRIVATE") || data.LiveSuiteAuto {
				t.Fatal("unsafe comparison inspection", data.LiveSuiteComparisonError)
			}
			if mode == "invalid-baseline" && calls != 0 || mode != "invalid-baseline" && calls != 1 {
				t.Fatal("unexpected backend operations", calls)
			}
		})
	}
}
