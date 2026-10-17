package apidserver

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUploadSubmissionReceiptBeforeSpoolCommit(t *testing.T) {
	e := setup(t, api.PlanPro)
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "upload-receipt"})
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("d", 32)
	dep, err := e.store.CreateDeployment(state.ContextWithDeploymentSubmission(t.Context(), id), state.Deployment{AppID: app.ID, Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/v1/uploads/unused/commit", nil)
	r.Header.Set(api.DeploymentAttemptHeader, id)
	w := httptest.NewRecorder()
	if !e.s.replayUploadSubmission(w, r, app.Slug) {
		t.Fatal("accepted upload was not recovered")
	}
	if w.Code != 202 || !strings.Contains(w.Body.String(), dep.ID) {
		t.Fatalf("reply=%d %s", w.Code, w.Body)
	}
	deployments, err := e.store.ListDeploymentsForApp(t.Context(), app.ID, 10, 0)
	if err != nil || len(deployments) != 1 {
		t.Fatalf("deployments=%v err=%v", deployments, err)
	}
}
func TestUploadSubmissionRejectsInvalidIdentity(t *testing.T) {
	for _, values := range [][]string{{"invalid"}, {strings.Repeat("a", 32), strings.Repeat("b", 32)}} {
		r := httptest.NewRequest("POST", "/v1/uploads/unused/commit", nil)
		for _, value := range values {
			r.Header.Add(api.DeploymentAttemptHeader, value)
		}
		w := httptest.NewRecorder()
		_, ok := uploadSubmissionAttemptID(w, r)
		if ok || w.Code != 400 {
			t.Fatalf("headers=%v accepted=%v code=%d", values, ok, w.Code)
		}
	}
}
