package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

// adr: 1330
// TestInteractiveImageRunGateAndToken drives POST /v1/image-runs through the
// routed handler: interactive runs need their own gate, a refusal by that
// gate is not replayed once it is enabled, and every created run hands out a
// fresh attach token even under one Idempotency-Key.
func TestInteractiveImageRunGateAndToken(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	srv, store, acct := *e.S(), *e.Store(), *e.Acct()
	srv.WithImageRunsEnabled(true)
	post := func(key string) (*httptest.ResponseRecorder, api.ImageRunResponse) {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/v1/image-runs",
			strings.NewReader(`{"image":"alpine","args":["sh"],"interactive":true,"tty":true}`))
		r.Header.Set("Authorization", "Bearer "+(*e.Key()))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		(*e.H()).ServeHTTP(rec, r)
		var resp api.ImageRunResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		return rec, resp
	}
	if rec, _ := post("pull"); rec.Code != http.StatusNotImplemented || !strings.Contains(rec.Body.String(), api.CodeNotImplemented) {
		t.Fatalf("interactive gate off: %d %s", rec.Code, rec.Body)
	}
	srv.WithInteractiveImageRunsEnabled(true)
	rec, pulling := post("pull")
	if rec.Code != http.StatusAccepted || pulling.Status != api.ImageRunStatusPulling || pulling.Attach != nil {
		t.Fatalf("after enabling the gate: %d %s; want a fresh 202 pull", rec.Code, rec.Body)
	}

	ctx := t.Context()
	job, err := store.JobGetByName(ctx, acct.ID, pulling.Job)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.JobClaimImageMaterialization(ctx, job.ID, "test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.JobRecordImageConfig(ctx, job.ID, job.ImageRef, "test", claimed.ImageMaterializationAttempts, nil, []string{"sh"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.JobPublishImageMaterialization(ctx, job.ID, job.ImageRef, "test", claimed.ImageMaterializationAttempts,
		"sha256:"+strings.Repeat("e", 64), "jobs/"+job.ID+"__test.ext4"); err != nil {
		t.Fatal(err)
	}

	first, firstResp := post("run")
	second, secondResp := post("run")
	for i, rec := range []*httptest.ResponseRecorder{first, second} {
		if rec.Code != http.StatusCreated || rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Idempotent-Replayed") != "" {
			t.Fatalf("run %d = %d headers=%v %s", i+1, rec.Code, rec.Header(), rec.Body)
		}
	}
	if firstResp.Attach == nil || secondResp.Attach == nil || firstResp.Attach.Token == secondResp.Attach.Token ||
		firstResp.RunID == secondResp.RunID {
		t.Fatalf("runs share a credential: %+v / %+v", firstResp.Attach, secondResp.Attach)
	}
	if want := api.JobTaskAttachPath(firstResp.Job, firstResp.RunID, 0); firstResp.Attach.Path != want {
		t.Fatalf("attach path = %q, want %q", firstResp.Attach.Path, want)
	}
	if _, err := store.JobRunAttachSession(ctx, firstResp.RunID); err != nil {
		t.Fatalf("session for the first run: %v", err)
	}
}
