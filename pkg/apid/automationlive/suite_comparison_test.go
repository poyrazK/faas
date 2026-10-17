// adr: 1298
package automationlive

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// This narrow fake cannot admit runs or check assertions.
type suiteComparisonHTTPStore struct {
	err error
}

func (s suiteComparisonHTTPStore) CompareAutomationLiveSuites(_ context.Context, app, name, candidate, baseline string) (api.AutomationLiveSuiteComparison, error) {
	if app != "app-id" || name != "report" || candidate != "candidate-id" || baseline != "baseline-id" {
		return api.AutomationLiveSuiteComparison{}, errors.New("unexpected scope")
	}
	return api.AutomationLiveSuiteComparison{Summary: api.AutomationLiveSuiteComparisonCounts{Total: 1, Regressions: 1}}, s.err
}

func TestSuiteComparisonHTTPScopedReadAndSafeErrors(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want int
	}{{"ok", nil, 200}, {"missing", state.ErrNotFound, 404}, {"backend", errors.New("PRIVATE backend detail"), 503}} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/unused", nil)
			r.SetPathValue("name", "report")
			r.SetPathValue("suite_id", "candidate-id")
			r.SetPathValue("baseline_id", "baseline-id")
			w := httptest.NewRecorder()
			SuiteComparisonService{Store: suiteComparisonHTTPStore{err: test.err}}.Compare(w, r, "app-id")
			if w.Code != test.want || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "PRIVATE") {
				t.Fatal("unsafe comparison response", w.Code, w.Body.String())
			}
		})
	}
}
