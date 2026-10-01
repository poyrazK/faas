package faas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestJobComputeRegionSDKRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req faas.CreateJobRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ComputeRegion != "eu-jobs" {
			t.Errorf("request=%+v %v", req, err)
		}
		_ = json.NewEncoder(w).Encode(faas.JobResponse{Name: req.Name, ComputeRegion: req.ComputeRegion})
	}))
	defer srv.Close()
	client, err := faas.NewClient(srv.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	job, err := client.CreateJob(context.Background(), faas.CreateJobRequest{Name: "regional-jobs", ImageRef: "registry.example/job:v1", Command: []string{"/bin/true"}, ComputeRegion: "eu-jobs"})
	if err != nil || job.ComputeRegion != "eu-jobs" {
		t.Fatalf("job=%+v %v", job, err)
	}
}
