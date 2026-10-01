package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCLIJobComputeRegionCreateAndDisplay(t *testing.T) {
	var captured api.CreateJobRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Error(err)
		}
		_ = json.NewEncoder(w).Encode(api.JobResponse{Name: captured.Name, ComputeRegion: captured.ComputeRegion})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	_, restore := captureStdout(t)
	defer restore()
	if code := cmdJobsAdd([]string{"regional-jobs", "--image", "registry.example/job:v1", "--command", "/bin/true", "--region", "eu-jobs"}); code != 0 || captured.ComputeRegion != "eu-jobs" {
		t.Fatalf("code=%d request=%+v", code, captured)
	}
	var out bytes.Buffer
	renderJobState(&out, api.JobResponse{Name: "regional-jobs", ComputeRegion: "eu-jobs"})
	if !strings.Contains(out.String(), "region:") || !strings.Contains(out.String(), "eu-jobs") {
		t.Fatalf("region missing from job info: %s", out.String())
	}
}
