package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestApplyProject_StalePlanTokenWritesOneProblem pins issue #2172: a stale
// plan token must produce one complete RFC 7807 document, not a directly
// written stale-token problem followed by the handler's second error.
func TestApplyProject_StalePlanTokenWritesOneProblem(t *testing.T) {
	spool := t.TempDir()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", spool)
	t.Setenv("FAAS_SPOOL_ROOT", spool)
	e, _ := newTestServerWithCapturingNotifier(t, api.PlanPro)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("source", "myapp.tar.gz")
	if err != nil {
		t.Fatalf("create source part: %v", err)
	}
	if _, err := fw.Write(applyProjectOneWorkloadTarGz(t)); err != nil {
		t.Fatalf("write source part: %v", err)
	}
	if err := mw.WriteField("project_slug", "stale-token"); err != nil {
		t.Fatalf("write project slug: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/projects?plan_token=bogus-token", &body)
	req.Header.Set("Authorization", "Bearer "+e.key)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("apply status = %d, want %d; body=%s", rec.Code, http.StatusConflict, rec.Body.String())
	}

	dec := json.NewDecoder(bytes.NewReader(rec.Body.Bytes()))
	var problem api.Problem
	if err := dec.Decode(&problem); err != nil {
		t.Fatalf("decode stale-token problem: %v; body=%s", err, rec.Body.String())
	}
	if problem.Code != "plan_token_stale" {
		t.Fatalf("problem code = %q, want plan_token_stale", problem.Code)
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		t.Fatalf("response contains more than one JSON document: decode=%v extra=%s body=%s", err, extra, rec.Body.String())
	}
}
