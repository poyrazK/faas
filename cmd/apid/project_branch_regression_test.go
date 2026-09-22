package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestProjectBranchAcceptsHyphensAndRejectsControls(t *testing.T) {
	for _, branch := range []string{"release/v1-2", "feature/my-api", "main"} {
		if !validProjectBranch(branch) {
			t.Errorf("rejected valid branch %q", branch)
		}
	}
	for c := rune(0); c <= 0x20; c++ {
		branch := "release" + string(c) + "candidate"
		if validProjectBranch(branch) {
			t.Errorf("accepted branch with control/space %q", branch)
		}
	}
}

func TestUpdateProjectAcceptsHyphenatedProductionBranch(t *testing.T) {
	srv, _, acct, _, _ := newProjectLifecycleFixture(t)
	req, rec := projectRequest(http.MethodPatch, "/v1/projects/shop", "shop", []byte(`{"production_branch":"release/v1-2"}`))
	srv.updateProject(rec, req, acct)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"production_branch":"release/v1-2"`) {
		t.Fatalf("update status=%d body=%s", rec.Code, rec.Body.String())
	}
}
