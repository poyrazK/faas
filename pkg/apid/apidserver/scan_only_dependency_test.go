package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

const onlyDependencySource = "services:\n  store:\n    image: nginx:1.27\n    ports: [\"8080:80\"]\n  compactor:\n    image: nginx:1.27\n    ports: [\"8080:80\"]\n    depends_on: [store]\n  query:\n    image: nginx:1.27\n    ports: [\"8080:80\"]\n    depends_on: [store]\n"

// ChronoLake finding 10: --only must not report a dependency on an
// unselected service of the same source as unknown.
func TestScanProjectOnlyKeepsUnselectedDependencyTargets(t *testing.T) {
	spool := t.TempDir()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", spool)
	t.Setenv("FAAS_SPOOL_ROOT", spool)
	e, _ := apidserver.NewTestServerWithCapturingNotifier(t, api.PlanPro)
	for _, selector := range []map[string]string{
		{"project_slug": "lake", "only": "compactor,query"},
		{"project_slug": "lake", "exclude": "store"},
	} {
		req := scanAuditRegressionRequest(t, *e.Key(), environmentProjectArchive(t, onlyDependencySource), selector)
		rec := httptest.NewRecorder()
		(*e.H()).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%v: scan status = %d; body=%s", selector, rec.Code, rec.Body.String())
		}
		var plan apidserver.ScanPlanResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &plan); err != nil {
			t.Fatal(err)
		}
		for _, reason := range plan.CanApplyReasons {
			if strings.Contains(reason, "unknown service") {
				t.Fatalf("%v: dependency on unselected store rejected: %v", selector, plan.CanApplyReasons)
			}
		}
		for _, w := range plan.Workloads {
			if w.Name == "store" {
				t.Fatalf("%v: unselected store entered the plan: %#v", selector, plan.Workloads)
			}
		}
	}
}
