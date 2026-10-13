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

// adr: 1161
func TestImageRunRouteIsGatedAndRunnerJobsAreManaged(t *testing.T) {
	e, notifier := apidserver.NewTestServerWithCapturingNotifier(t, api.PlanPro)
	srv, acct := *e.S(), *e.Acct()
	post := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		srv.CreateImageRun(rec, httptest.NewRequest(http.MethodPost, "/v1/image-runs",
			strings.NewReader(`{"image":"alpine","args":["true"]}`)), acct)
		return rec
	}
	if rec := post(); rec.Code != http.StatusNotFound {
		t.Fatalf("gate off: %d %s", rec.Code, rec.Body)
	}
	srv.WithImageRunsEnabled(true)
	rec := post()
	var resp api.ImageRunResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || rec.Code != http.StatusAccepted || resp.Status != api.ImageRunStatusPulling {
		t.Fatalf("gate on: %d %s (%v)", rec.Code, rec.Body, err)
	}
	(*notifier.Mu()).Lock()
	var pulled bool
	for _, n := range *notifier.Emitted() {
		pulled = pulled || strings.Contains(n.Payload, `"kind":"image_run"`)
	}
	(*notifier.Mu()).Unlock()
	if !pulled {
		t.Fatal("imaged was not told to pull the image")
	}

	rec = httptest.NewRecorder()
	srv.ListJobs(rec, httptest.NewRequest(http.MethodGet, "/v1/jobs", nil), acct)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), resp.Job) {
		t.Fatalf("runner job listed: %d %s", rec.Code, rec.Body)
	}
	req := httptest.NewRequest(http.MethodPatch, "/v1/jobs/"+resp.Job, strings.NewReader(`{"ram_mb":256}`))
	req.SetPathValue("name", resp.Job)
	rec = httptest.NewRecorder()
	srv.UpdateJob(rec, req, acct)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), api.CodeImageRunManaged) {
		t.Fatalf("PATCH runner job: %d %s", rec.Code, rec.Body)
	}
}
