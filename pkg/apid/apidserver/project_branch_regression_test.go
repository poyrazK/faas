package apidserver_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestProjectBranchAcceptsHyphensAndRejectsControls(t *testing.T) {
	for _, branch := range []string{"release/v1-2", "feature/my-api", "main"} {
		if !apidserver.ValidProjectBranch(branch) {
			t.Errorf("rejected valid branch %q", branch)
		}
	}
	for c := rune(0); c <= 0x20; c++ {
		branch := "release" + string(c) + "candidate"
		if apidserver.ValidProjectBranch(branch) {
			t.Errorf("accepted branch with control/space %q", branch)
		}
	}
}

func TestUpdateProjectAcceptsHyphenatedProductionBranch(t *testing.T) {
	srv, _, acct, _, _ := apidserver.NewProjectLifecycleFixture(t)
	req, rec := apidserver.ProjectRequest(http.MethodPatch, "/v1/projects/shop", "shop", []byte(`{"production_branch":"release/v1-2"}`))
	srv.UpdateProject(rec, req, acct)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"production_branch":"release/v1-2"`) {
		t.Fatalf("update status=%d body=%s", rec.Code, rec.Body.String())
	}
}
