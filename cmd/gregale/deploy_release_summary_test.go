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

func TestRenderSuccessfulDeploymentUsesCanonicalAppURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/deployments/d1":
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", AppID: "a1", Status: statusLive})
		case "/v1/apps/my-app":
			_ = json.NewEncoder(w).Encode(api.AppResponse{
				Slug: "my-app", URL: "https://my-app.gregale.dev", CanonicalURL: "https://custom.example.com",
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
	if !strings.Contains(out.String(), "Deployed. https://custom.example.com") {
		t.Errorf("deploy output missing canonical URL\nfull output:\n%s", out.String())
	}
}

func TestRenderSuccessfulDarkDeploymentShowsPreviewAndPromotion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/deployments/d1":
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", AppID: "a1", Status: statusLive, Revision: 44, TrafficPercent: 0})
		case "/v1/deployments/d1/url":
			_ = json.NewEncoder(w).Encode(api.DeploymentPreviewURL{DeploymentID: "d1", URL: "https://deploy-44-my-app.gregale.dev", Alive: true})
		case "/v1/apps/my-app":
			_ = json.NewEncoder(w).Encode(api.AppResponse{Slug: "my-app", CanonicalURL: "https://my-app.gregale.dev"})
		case "/v1/apps/my-app/deployments":
			_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{Items: []api.DeploymentResponse{
				{ID: "d1", Revision: 44, Status: statusLive, TrafficPercent: 0},
				{ID: "d0", Revision: 43, Status: statusLive, TrafficPercent: 100},
			}})
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	var out bytes.Buffer
	oldOut := osStdout
	osStdout = &out
	defer func() { osStdout = oldOut }()

	if code := renderSuccessfulDeploymentWithOptions(context.Background(), api.NewClient(srv.URL, "fp_live_x"), api.DeploymentResponse{ID: "d1", Status: statusLive}, "my-app", true); code != 0 {
		t.Fatalf("renderSuccessfulDeploymentWithOptions exit = %d, want 0", code)
	}
	for _, want := range []string{
		"Staged v44 with 0% production traffic.",
		"Preview: https://deploy-44-my-app.gregale.dev",
		"Production remains on v43. https://my-app.gregale.dev",
		"Promote: gregale traffic promote --app my-app --deployment v44 --if-serving v43",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("dark deploy output missing %q\nfull output:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "Deployed v44") || strings.Contains(out.String(), "Release summary:") {
		t.Errorf("dark deploy rendered normal release copy\nfull output:\n%s", out.String())
	}
}

func TestDeploymentPromotionCommandOmitsSplitTraffic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/my-app/deployments" {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{Items: []api.DeploymentResponse{
			{ID: "candidate", Revision: 44, Status: statusLive, TrafficPercent: 0},
			{ID: "stable-a", Revision: 43, Status: statusLive, TrafficPercent: 50},
			{ID: "stable-b", Revision: 42, Status: statusLive, TrafficPercent: 50},
		}})
	}))
	defer srv.Close()
	command, serving := deploymentPromotionCommand(context.Background(), api.NewClient(srv.URL, "fp_live_x"),
		"my-app", api.DeploymentResponse{ID: "candidate", Revision: 44, Status: statusLive})
	if command != "" || serving != "" {
		t.Fatalf("split traffic yielded promotion command %q serving %q, want neither", command, serving)
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

func TestWriteWaitedDarkDeploymentReceiptIncludesPreviewAndPromotion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/deployments/d1":
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", AppID: "a1", Status: statusLive, Revision: 44, TrafficPercent: 0})
		case "/v1/deployments/d1/url":
			_ = json.NewEncoder(w).Encode(api.DeploymentPreviewURL{DeploymentID: "d1", URL: "https://deploy-44-my-app.gregale.dev", Alive: true})
		case "/v1/apps/my-app/deployments":
			_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{Items: []api.DeploymentResponse{
				{ID: "d1", Revision: 44, Status: statusLive, TrafficPercent: 0},
				{ID: "d0", Revision: 43, Status: statusLive, TrafficPercent: 100},
			}})
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

	if code := writeWaitedDeploymentReceiptUntilWithOptions(
		context.Background(), api.NewClient(srv.URL, "fp_live_x"),
		api.DeploymentResponse{ID: "d1", Status: statusLive}, nil,
		"https://my-app.gregale.dev", "", "my-app", time.Second, false, true,
	); code != 0 {
		t.Fatalf("writeWaitedDeploymentReceiptUntilWithOptions exit = %d, want 0", code)
	}

	var receipt DeployReceipt
	if err := json.Unmarshal(out.Bytes(), &receipt); err != nil {
		t.Fatalf("decode dark deploy receipt: %v\noutput: %s", err, out.String())
	}
	if receipt.PreviewURL != "https://deploy-44-my-app.gregale.dev" {
		t.Fatalf("preview_url = %q", receipt.PreviewURL)
	}
	if receipt.PromotionCommand != "gregale traffic promote --app my-app --deployment v44 --if-serving v43" {
		t.Fatalf("promotion_command = %q", receipt.PromotionCommand)
	}
	if receipt.ReleaseSummary != nil {
		t.Fatalf("dark receipt unexpectedly contains release_summary: %+v", receipt.ReleaseSummary)
	}
}

// TestRenderDeploymentReleaseSummary_UsesRevisionHandle pins the ADR-198
// surface on the one line of deploy output that is meant to be copy-pasted.
// A uuid is the single thing here a human cannot retype or recognise later,
// so when the rollback target has a revision the command must name it.
//
// adr: 198
func TestRenderDeploymentReleaseSummary_UsesRevisionHandle(t *testing.T) {
	var out bytes.Buffer
	renderDeploymentReleaseSummary(&out, api.DeploymentSummaryResponse{
		Previous: &api.DeploymentResponse{ID: "8f14e45fceea467a9c8e9b0e21c6d5a1", Revision: 41},
		Changes: []api.DeploymentChange{{
			Field: "image_digest", Before: "sha256:old", After: "sha256:new",
		}},
		RollbackTargetID:       "8f14e45fceea467a9c8e9b0e21c6d5a1",
		RollbackTargetRevision: 41,
	}, "my-app")

	got := out.String()
	if !strings.Contains(got, "Rollback: gregale rollback my-app --to v41") {
		t.Errorf("rollback command does not use the v41 handle\nfull output:\n%s", got)
	}
	if !strings.Contains(got, "Changes since v41:") {
		t.Errorf("change header does not use the v41 handle\nfull output:\n%s", got)
	}
	// The uuid must not leak into output that already names the revision —
	// printing both is what made the original line unreadable.
	if strings.Contains(got, "8f14e45fceea467a9c8e9b0e21c6d5a1") {
		t.Errorf("uuid still rendered alongside the revision\nfull output:\n%s", got)
	}
}

// TestRenderDeploymentReleaseSummary_FallsBackToIDWithoutRevision pins that a
// row predating the revision column still prints a WORKING command. The
// fallback is the reason RollbackTargetID stays on the wire next to the
// revision; rendering `v0` here would print a handle that cannot resolve.
//
// adr: 198
func TestRenderDeploymentReleaseSummary_FallsBackToIDWithoutRevision(t *testing.T) {
	var out bytes.Buffer
	renderDeploymentReleaseSummary(&out, api.DeploymentSummaryResponse{
		Previous:         &api.DeploymentResponse{ID: "legacy-release"},
		Changes:          []api.DeploymentChange{},
		RollbackTargetID: "legacy-release",
	}, "my-app")

	got := out.String()
	if !strings.Contains(got, "Rollback: gregale rollback my-app --to legacy-release") {
		t.Errorf("revision-less target did not fall back to the id\nfull output:\n%s", got)
	}
	if strings.Contains(got, "v0") {
		t.Errorf("revision-less target rendered as v0\nfull output:\n%s", got)
	}
}
