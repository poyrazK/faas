package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestProjectsEnvironmentPromoteWaitReturnsSingleJSONReceipt(t *testing.T) {
	resetJSONOut(t)
	oldOut, oldErr := osStdout, osStderr
	var out, errOut bytes.Buffer
	osStdout, osStderr = &out, &errOut
	jsonOutput = true
	t.Cleanup(func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, false })

	oldInterval := projectEnvironmentPromotionPollInterval
	projectEnvironmentPromotionPollInterval = time.Millisecond
	t.Cleanup(func() { projectEnvironmentPromotionPollInterval = oldInterval })

	statusCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/promotion-preview"):
			writeJSONTest(w, api.ProjectEnvironmentPromotionPreviewResponse{
				ProjectSlug: "shop", FromEnvironment: "staging", ToEnvironment: "production",
				CanPromote: true, PromotionHash: "hash", PromotionToken: "token",
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/promote"):
			writeJSONTest(w, api.ProjectEnvironmentPromotionResponse{
				PromotionID: "prom-1", ProjectSlug: "shop", FromEnvironment: "staging",
				ToEnvironment: "production", PromotionHash: "hash",
				Workloads: []api.ProjectEnvironmentPromotionWorkloadResponse{{WorkloadSlug: "api", Status: "promoted"}},
			})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/promotions/prom-1"):
			statusCalls++
			status := "running"
			verification := "verifying"
			if statusCalls > 1 {
				status = "succeeded"
				verification = "verified"
			}
			writeJSONTest(w, api.ProjectEnvironmentPromotionStatusResponse{
				PromotionID: "prom-1", ProjectSlug: "shop", FromEnvironment: "staging",
				ToEnvironment: "production", PromotionHash: "hash", Status: status,
				VerificationStatus: verification,
				Workloads:          []api.ProjectEnvironmentPromotionStatusWorkloadResponse{{WorkloadSlug: "api", Status: "promoted", VerificationStatus: verification}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if code := cmdProjectsEnvironmentPromote([]string{"shop", "--from", "staging", "--to", "production", "--yes", "--wait", "--progress", "--timeout", "1"}); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, errOut.String())
	}
	if statusCalls != 2 {
		t.Fatalf("status calls = %d, want 2", statusCalls)
	}
	var receipt projectEnvironmentPromotionWaitReceipt
	decoder := json.NewDecoder(&out)
	if err := decoder.Decode(&receipt); err != nil {
		t.Fatalf("decode receipt: %v; output=%q", err, out.String())
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatalf("JSON output contains more than one document: err=%v output=%q", err, out.String())
	}
	if !receipt.Succeeded || receipt.TimedOut || receipt.Promotion.Status != "succeeded" {
		t.Fatalf("receipt = %+v, want succeeded non-timeout", receipt)
	}
	if errOut.Len() != 0 {
		t.Fatalf("JSON progress leaked to stderr: %q", errOut.String())
	}
}

func TestWaitForProjectEnvironmentPromotionTimesOutWithLastStatus(t *testing.T) {
	oldInterval := projectEnvironmentPromotionPollInterval
	projectEnvironmentPromotionPollInterval = time.Millisecond
	t.Cleanup(func() { projectEnvironmentPromotionPollInterval = oldInterval })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSONTest(w, api.ProjectEnvironmentPromotionStatusResponse{
			PromotionID: "prom-1", ProjectSlug: "shop", ToEnvironment: "production", Status: "running",
		})
	}))
	defer srv.Close()

	status, timedOut, err := waitForProjectEnvironmentPromotion(
		context.Background(), NewClient(srv.URL, "test-token"), "shop", "production", "prom-1",
		10*time.Millisecond, api.ProjectEnvironmentPromotionStatusResponse{PromotionID: "prom-1", ProjectSlug: "shop", ToEnvironment: "production", Status: "running"}, nil,
	)
	if err != nil {
		t.Fatalf("wait error = %v", err)
	}
	if !timedOut || status.Status != "running" {
		t.Fatalf("status=%+v timedOut=%v, want running/true", status, timedOut)
	}
}
