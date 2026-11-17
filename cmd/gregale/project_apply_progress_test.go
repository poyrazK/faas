package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// projectProgressServer serves two deployments: orders builds then fails,
// cart builds then goes live.
func projectProgressServer(t *testing.T) *httptest.Server {
	t.Helper()
	var ordersReads, cartReads atomic.Int32
	building := json.RawMessage(`{"current":"image_build","history":[]}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var dep api.DeploymentResponse
		switch r.URL.Path {
		case "/v1/deployments/orders":
			dep = api.DeploymentResponse{ID: "orders", Status: "building", StageState: building}
			if ordersReads.Add(1) > 1 {
				dep = api.DeploymentResponse{ID: "orders", Status: "failed", Error: "cold_boot_timeout: instance=i-1"}
			}
		case "/v1/deployments/cart":
			dep = api.DeploymentResponse{ID: "cart", Status: "building", StageState: building}
			if cartReads.Add(1) > 2 {
				dep = api.DeploymentResponse{ID: "cart", Status: "live", RolloutState: "complete", TrafficPercent: 100}
			}
		default:
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(dep)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// progressApply is a fresh receipt per call: the wait writes results into
// its Builds.
func progressApply() api.ApplyResponse {
	return api.ApplyResponse{Builds: []api.AppliedBuild{
		{Slug: "retail-demo-orders", DeploymentID: "orders"},
		{Slug: "retail-demo-cart", DeploymentID: "cart"},
	}}
}

// GREG-04: the project wait printed nothing between upload and the final
// receipt, and a failed upstream appeared only at the end.
func TestProjectWaitReportsStagesAndFailuresAsTheyHappen(t *testing.T) {
	srv := projectProgressServer(t)
	var stderr bytes.Buffer
	ctx := main.WithProjectWaitProgress(t.Context(), &stderr, false)
	main.WaitForProjectApplyTimeouts(ctx, main.NewClient(srv.URL, "token"), progressApply(), 20*time.Second)

	got := stderr.String()
	for _, want := range []string{
		"retail-demo-orders: image_build",
		"retail-demo-orders: failed after",
		"cold_boot_timeout",
		"retail-demo-cart: image_build",
		"retail-demo-cart: live after",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("progress lacks %q:\n%s", want, got)
		}
	}
	// The orders failure is reported before cart finishes.
	if strings.Index(got, "retail-demo-orders: failed") > strings.Index(got, "retail-demo-cart: live") {
		t.Errorf("orders failure reported after cart went live:\n%s", got)
	}
	// Repeated reads of an unchanged stage print once.
	if n := strings.Count(got, "retail-demo-cart: image_build"); n != 1 {
		t.Errorf("cart image_build printed %d times:\n%s", n, got)
	}
}

func TestProjectWaitJSONProgressEvents(t *testing.T) {
	srv := projectProgressServer(t)
	var stderr bytes.Buffer
	ctx := main.WithProjectWaitProgress(t.Context(), &stderr, true)
	main.WaitForProjectApplyTimeouts(ctx, main.NewClient(srv.URL, "token"), progressApply(), 20*time.Second)

	stages := map[string][]string{}
	for _, line := range strings.Split(strings.TrimSpace(stderr.String()), "\n") {
		var ev struct {
			Event   string `json:"event"`
			Service string `json:"service"`
			Stage   string `json:"stage"`
			Error   string `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("progress line is not JSON: %q", line)
		}
		if ev.Event != "service_stage_changed" {
			t.Fatalf("event = %q", ev.Event)
		}
		stages[ev.Service] = append(stages[ev.Service], ev.Stage)
	}
	if strings.Join(stages["retail-demo-orders"], ",") != "image_build,failed" || strings.Join(stages["retail-demo-cart"], ",") != "image_build,live" {
		t.Fatalf("stages = %v\nstderr:\n%s", stages, stderr.String())
	}
}

// A wait without a progress reporter stays silent (other callers).
func TestProjectWaitWithoutProgressIsSilent(t *testing.T) {
	srv := projectProgressServer(t)
	var stderr bytes.Buffer
	old := *main.OsStderr
	*main.OsStderr = &stderr
	defer func() { *main.OsStderr = old }()
	main.WaitForProjectApplyTimeouts(t.Context(), main.NewClient(srv.URL, "token"), progressApply(), 20*time.Second)
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
