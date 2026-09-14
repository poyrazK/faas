package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestProjectSourceRefScanUsesConnectedInstallationWithoutMutation(t *testing.T) {
	e := newSourceRefTestServer(t, api.PlanPro, "existing", 9999)
	e.gh.streamBody = nopReadCloser{Reader: bytes.NewReader(e.tarball)}
	rec := e.post(t, "/v1/projects/scan/source-ref", api.ProjectSourceRefScanRequest{
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
	if e.gh.streamCalls != 1 || e.gh.streamInstID != 9999 || e.gh.streamRepo != "onebox-faas/hello" || e.gh.streamRef != "main" {
		t.Fatalf("stream call = calls:%d install:%d repo:%q ref:%q", e.gh.streamCalls, e.gh.streamInstID, e.gh.streamRepo, e.gh.streamRef)
	}
	if len(e.gh.listRepoCalls) != 1 || e.gh.listRepoCalls[0] != 9999 {
		t.Fatalf("installation resolution calls = %#v, want [9999]", e.gh.listRepoCalls)
	}
	projects, err := e.store.ListProjectsForAccount(context.Background(), e.acctID)
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}
	if len(projects) != 0 {
		t.Fatalf("scan mutated projects: %#v", projects)
	}
}

func TestProjectSourceRefScanRejectsInvalidInputBeforeFetch(t *testing.T) {
	e := newSourceRefTestServer(t, api.PlanPro, "existing", 9999)
	rec := e.post(t, "/v1/projects/scan/source-ref", api.ProjectSourceRefScanRequest{
		Repo: "onebox-faas/hello", Ref: "../main", ProjectSlug: "hello",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body)
	}
	if e.gh.streamCalls != 0 || len(e.gh.listRepoCalls) != 0 {
		t.Fatalf("invalid input reached GitHub: stream=%d list=%#v", e.gh.streamCalls, e.gh.listRepoCalls)
	}
}
