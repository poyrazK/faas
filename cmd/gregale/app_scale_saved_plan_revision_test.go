// adr: 590
package main_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// A saved stage plan applies only while the stage is still at the revision
// the plan was built on: pinning the first read must not hide a newer one.
func TestAppScaleSavedEnvironmentPlanRefusesNewerRevision(t *testing.T) {
	main.ResetJSONOut(t)
	(*main.JsonOutput) = true
	revision, writes := "7", 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/hello":
			w.Header().Set("X-Gregale-Workload-Revision", revision)
			main.WriteJSONTest(w, api.AppResponse{Slug: "hello", RAMMB: 256})
		case r.Method == http.MethodPatch:
			writes++
			main.WriteJSONTest(w, api.AppResponse{Slug: "hello"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	path := filepath.Join(t.TempDir(), "scale-plan.json")
	if code := main.CmdAppScale("hello", []string{"--plan", "--environment", "staging", "--ram", "512", "--out", path}); code != 0 {
		t.Fatalf("plan exit = %d", code)
	}
	revision = "8"
	if code := main.CmdAppScale("hello", []string{"--apply", path, "--confirm"}); code == 0 || writes != 0 {
		t.Fatalf("stale saved plan applied: exit=%d writes=%d", code, writes)
	}
}
