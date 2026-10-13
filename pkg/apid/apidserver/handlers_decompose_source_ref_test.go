package apidserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestProjectSourceRefScanUsesConnectedInstallationWithoutMutation(t *testing.T) {
	e := apidserver.NewSourceRefTestServer(t, api.PlanPro, "existing", 9999)
	(*(*e.Gh()).StreamBody()) = apidserver.NopReadCloser{Reader: bytes.NewReader((*e.Tarball()))}
	rec := e.Post(t, "/v1/projects/scan/source-ref", api.ProjectSourceRefScanRequest{
		Repo: "onebox-faas/hello", Ref: "main", ProjectSlug: "hello",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var plan api.PlanResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &plan); err != nil {
		t.Fatalf("decode plan: %v", err)
	}
	if plan.ProjectSlug != "hello" {
		t.Fatalf("project_slug = %q, want hello", plan.ProjectSlug)
	}
	if (*(*e.Gh()).StreamCalls()) != 1 || (*(*e.Gh()).StreamInstID()) != 9999 || (*(*e.Gh()).StreamRepo()) != "onebox-faas/hello" || (*(*e.Gh()).StreamRef()) != "main" {
		t.Fatalf("stream call = calls:%d install:%d repo:%q ref:%q", (*(*e.Gh()).StreamCalls()), (*(*e.Gh()).StreamInstID()), (*(*e.Gh()).StreamRepo()), (*(*e.Gh()).StreamRef()))
	}
	if len((*(*e.Gh()).ListRepoCalls())) != 1 || (*(*e.Gh()).ListRepoCalls())[0] != 9999 {
		t.Fatalf("installation resolution calls = %#v, want [9999]", (*(*e.Gh()).ListRepoCalls()))
	}
	projects, err := (*e.Store()).ListProjectsForAccount(context.Background(), (*e.AcctID()))
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}
	if len(projects) != 0 {
		t.Fatalf("scan mutated projects: %#v", projects)
	}
}

func TestProjectSourceRefScanRejectsInvalidInputBeforeFetch(t *testing.T) {
	e := apidserver.NewSourceRefTestServer(t, api.PlanPro, "existing", 9999)
	rec := e.Post(t, "/v1/projects/scan/source-ref", api.ProjectSourceRefScanRequest{
		Repo: "onebox-faas/hello", Ref: "../main", ProjectSlug: "hello",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body)
	}
	if (*(*e.Gh()).StreamCalls()) != 0 || len((*(*e.Gh()).ListRepoCalls())) != 0 {
		t.Fatalf("invalid input reached GitHub: stream=%d list=%#v", (*(*e.Gh()).StreamCalls()), (*(*e.Gh()).ListRepoCalls()))
	}
}
