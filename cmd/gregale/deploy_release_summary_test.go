package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestRenderDeploymentReleaseSummary(t *testing.T) {
	var out bytes.Buffer
	renderDeploymentReleaseSummary(&out, api.DeploymentSummaryResponse{
		Previous: &api.DeploymentResponse{ID: "previous-release"},
		Changes: []api.DeploymentChange{{
			Field: "image_digest", Before: "sha256:old", After: "sha256:new",
		}},
		RollbackTargetID: "previous-release",
	}, "my-app")

	got := out.String()
	for _, want := range []string{
		"Release summary:",
		"Changes since previous-release:",
		"image_digest       \"sha256:old\" -> \"sha256:new\"",
		"Rollback: gregale rollback my-app --to previous-release",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary missing %q\nfull output:\n%s", want, got)
		}
	}
}

func TestRenderSuccessfulDeploymentIncludesReleaseSummary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/deployments/d1":
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", AppID: "a1", Status: statusLive})
		case "/v1/apps/my-app/deployments/d1/summary":
			_ = json.NewEncoder(w).Encode(api.DeploymentSummaryResponse{
				Deployment:       api.DeploymentResponse{ID: "d1", Status: statusLive},
				Previous:         &api.DeploymentResponse{ID: "d0", Status: "superseded"},
				Changes:          []api.DeploymentChange{{Field: "commit_sha", Before: "old", After: "new"}},
				RollbackTargetID: "d0",
			})
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	var out bytes.Buffer
	oldOut := osStdout
	osStdout = &out
	defer func() { osStdout = oldOut }()

	if code := renderSuccessfulDeployment(context.Background(), api.NewClient(srv.URL, "fp_live_x"), api.DeploymentResponse{ID: "d1", Status: statusLive}, "my-app"); code != 0 {
		t.Fatalf("renderSuccessfulDeployment exit = %d, want 0", code)
	}
	for _, want := range []string{
		"Deployed. https://my-app.gregale.dev",
		"Changes since d0:",
		"commit_sha",
		"Rollback: gregale rollback my-app --to d0",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("deploy output missing %q\nfull output:\n%s", want, out.String())
		}
	}
}

func TestWriteWaitedDeploymentReceiptIncludesReleaseSummary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/deployments/d1":
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", AppID: "a1", Status: statusLive})
		case "/v1/apps/my-app/deployments/d1/summary":
			_ = json.NewEncoder(w).Encode(api.DeploymentSummaryResponse{
				Deployment:       api.DeploymentResponse{ID: "d1", Status: statusLive},
				Previous:         &api.DeploymentResponse{ID: "d0"},
				Changes:          []api.DeploymentChange{{Field: "image_digest", Before: "old", After: "new"}},
				RollbackTargetID: "d0",
			})
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	var out bytes.Buffer
	oldOut := osStdout
	osStdout = &out
	defer func() { osStdout = oldOut }()
	oldJSON := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = oldJSON }()

	if code := writeWaitedDeploymentReceiptUntil(
		context.Background(), api.NewClient(srv.URL, "fp_live_x"),
		api.DeploymentResponse{ID: "d1", Status: statusLive}, nil,
		"https://my-app.gregale.dev", "", "my-app", time.Second,
	); code != 0 {
		t.Fatalf("writeWaitedDeploymentReceiptUntil exit = %d, want 0", code)
	}

	var receipt DeployReceipt
	if err := json.Unmarshal(out.Bytes(), &receipt); err != nil {
		t.Fatalf("decode deploy receipt: %v\noutput: %s", err, out.String())
	}
	if receipt.ReleaseSummary == nil {
		t.Fatalf("release_summary missing from receipt: %s", out.String())
	}
	if receipt.ReleaseSummary.PreviousDeploymentID != "d0" || receipt.ReleaseSummary.RollbackCommand != "gregale rollback my-app --to d0" {
		t.Fatalf("release summary = %+v", receipt.ReleaseSummary)
	}
	if len(receipt.ReleaseSummary.Changes) != 1 || receipt.ReleaseSummary.Changes[0].Field != "image_digest" {
		t.Fatalf("release changes = %+v", receipt.ReleaseSummary.Changes)
	}
}
