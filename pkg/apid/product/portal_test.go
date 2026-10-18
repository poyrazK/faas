package product

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestProductGateDisabledUsesProblem(t *testing.T) {
	s := Service{}
	called := false
	w := httptest.NewRecorder()
	s.Gate(func(http.ResponseWriter, *http.Request) { called = true })(w, httptest.NewRequest(http.MethodGet, "/v1/platform-tenant-self/product", nil))
	var problem api.Problem
	if err := json.Unmarshal(w.Body.Bytes(), &problem); err != nil || w.Code != http.StatusNotFound || problem.Code != api.CodeNotFound || called {
		t.Fatalf("closed Product Control gate: %d %s %v", w.Code, w.Body.String(), err)
	}
}

func TestProductPortalBrowser(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js required for portal browser contract")
	}
	for _, script := range []string{"portal_core_test.cjs", "portal_test.cjs"} {
		output, err := exec.CommandContext(t.Context(), node, script).CombinedOutput()
		if err != nil {
			t.Fatalf("portal browser contract %s: %v\n%s", script, err, output)
		}
	}
}
