package apidserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// A project apply deploys each workload with the workflows its own
// gregale.yaml declares, as a single-app deploy of that directory does.
func TestProjectApplyDeploysWorkloadManifestWorkflows(t *testing.T) {
	s, store, acct, key := projectSecretsTestServer(t, api.ScopesAdminOnly)
	rec := projectSecretsRequest(t, s, key, "/v1/projects", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("initial=%d %s", rec.Code, rec.Body.String())
	}
	var first api.ApplyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	apps, err := store.MemStore.AppsForProject(t.Context(), acct.ID, first.ProjectID)
	if err != nil || len(apps) == 0 {
		t.Fatalf("apps=%+v %v", apps, err)
	}
	app := apps[0]
	root := t.TempDir()
	dir := filepath.Join(root, app.RootDir)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM alpine\n"), 0600); err != nil {
		t.Fatal(err)
	}
	manifest := "workflows:\n  - name: process_order\n    steps:\n      - name: charge\n        run: charge_order\n"
	if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	result := s.applyBuildsForAddedChangedOrdered(t.Context(), httptest.NewRequest("POST", "/v1/projects", nil), acct, state.Project{ID: first.ProjectID}, root,
		[]reposcan.Workload{{Name: app.WorkloadName}}, nil, nil, []state.App{app}, "production", "", nil)
	if len(result) != 1 || result[0].Error != "" {
		t.Fatalf("enqueue=%+v", result)
	}
	dep, err := store.MemStore.DeploymentByID(t.Context(), result[0].DeploymentID)
	if err != nil || !bytes.Contains(dep.Workflows, []byte("process_order")) {
		t.Fatalf("deployment workflows = %s, err=%v", dep.Workflows, err)
	}
}
