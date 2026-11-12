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

// A liveness-parked app showed "live" in release list and project info and
// the raw "evicted_cold" in status; only inspect said it was down
// (2026-11-12 agent feedback).

func livenessParkedApp(slug, id, deploymentID string) api.AppResponse {
	return api.AppResponse{ID: id, Slug: slug, Status: appStatusParked,
		ParkedDeployment: &api.ParkedDeploymentRef{ID: deploymentID, ParkedReason: "liveness_exhausted"}}
}

func captureParkedStdout(t *testing.T) *bytes.Buffer {
	t.Helper()
	var out bytes.Buffer
	previousOut, previousJSON := osStdout, jsonOutput
	osStdout, jsonOutput = &out, false
	t.Cleanup(func() { osStdout, jsonOutput = previousOut, previousJSON })
	return &out
}

func TestParkedStatusLabels(t *testing.T) {
	for reason, want := range map[string]string{
		"liveness_exhausted":      "parked: liveness failed",
		"security_scan_regressed": "parked: security scan",
		"lifecycle_park":          "parked",
		"":                        "parked",
	} {
		if got := parkedStatusLabel(reason); got != want {
			t.Errorf("parkedStatusLabel(%q) = %q, want %q", reason, got, want)
		}
	}
	if got := appParkedLabel(api.AppResponse{Status: "active"}); got != "" {
		t.Errorf("active app label = %q, want empty", got)
	}
	// The parked reference outlives the park: an active app is not parked.
	active := livenessParkedApp("web", "a1", "d1")
	active.Status = "active"
	if got := parkedDeploymentID(active); got != "" {
		t.Errorf("active app parked deployment = %q, want empty", got)
	}
	if got := formatAppListStatus(livenessParkedApp("web", "a1", "d1")); got != "parked: liveness failed" {
		t.Errorf("app list status = %q", got)
	}
}

func TestStatusNamesLivenessPark(t *testing.T) {
	out := captureParkedStdout(t)
	app := livenessParkedApp("retail-ui", "a1", "d1")
	renderCheckoutStatus(checkoutStatus{App: &app, Platform: "gregale status --platform"})
	got := out.String()
	for _, want := range []string{"State        parked: liveness failed\n", "! retail-ui is parked: its instances repeatedly failed liveness checks", "gregale inspect retail-ui"} {
		if !strings.Contains(got, want) {
			t.Fatalf("status missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "evicted_cold") {
		t.Fatalf("status printed the stored status:\n%s", got)
	}
}

func TestStatusPlainParkHasNoFailureNotice(t *testing.T) {
	out := captureParkedStdout(t)
	app := api.AppResponse{Slug: "web", Status: appStatusParked}
	renderCheckoutStatus(checkoutStatus{App: &app})
	if got := out.String(); !strings.Contains(got, "State        parked\n") || strings.Contains(got, "!") {
		t.Fatalf("plain park status:\n%s", got)
	}
}

func TestReleaseListMarksLiveReleaseOfParkedApp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/apps/retail-ui/deployments":
			_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{Items: []api.DeploymentResponse{
				{ID: "d2", AppID: "a1", Status: "live", Kind: "app", CreatedAt: "2026-11-12T18:38:00Z"},
				// An older release stamped with a past park is not the parked one.
				{ID: "d1", AppID: "a1", Status: "superseded", Kind: "app", CreatedAt: "2026-11-12T17:00:00Z", ParkedReason: "liveness_exhausted"},
			}})
		case "/v1/apps/retail-ui":
			_ = json.NewEncoder(w).Encode(livenessParkedApp("retail-ui", "a1", "d2"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	out := captureParkedStdout(t)
	if code := cmdDeployments([]string{"--app", "retail-ui"}); code != 0 {
		t.Fatalf("release list = %d\n%s", code, out.String())
	}
	got := out.String()
	lines := strings.Split(got, "\n")
	if !strings.Contains(lines[0], "d2") || !strings.Contains(lines[0], "live/parked") {
		t.Fatalf("live row not marked parked:\n%s", got)
	}
	if strings.Contains(lines[1], "parked") {
		t.Fatalf("superseded row marked parked:\n%s", got)
	}
	if strings.Count(got, "! retail-ui is parked") != 1 {
		t.Fatalf("want one parked notice:\n%s", got)
	}
}

func TestReleaseListAccountWideMarksOnlyParkedApps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/deployments":
			_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{Items: []api.DeploymentResponse{
				{ID: "d2", AppID: "a1", Status: "live", Kind: "app"},
				{ID: "d9", AppID: "a2", Status: "live", Kind: "app"},
			}})
		case "/v1/apps":
			_ = json.NewEncoder(w).Encode([]api.AppResponse{
				livenessParkedApp("retail-ui", "a1", "d2"),
				{ID: "a2", Slug: "retail-cart", Status: "active"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	out := captureParkedStdout(t)
	if code := cmdDeployments(nil); code != 0 {
		t.Fatalf("release list = %d\n%s", code, out.String())
	}
	lines := strings.Split(out.String(), "\n")
	if !strings.Contains(lines[0], "live/parked") || strings.Contains(lines[1], "parked") {
		t.Fatalf("account-wide marking wrong:\n%s", out.String())
	}
}

func TestReleaseListAppLookupFailureKeepsPlainList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/apps/web/deployments" {
			_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{Items: []api.DeploymentResponse{{ID: "d1", AppID: "a1", Status: "live", Kind: "app"}}})
			return
		}
		http.Error(w, `{"title":"boom"}`, http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	out := captureParkedStdout(t)
	if code := cmdDeployments([]string{"--app", "web"}); code != 0 {
		t.Fatalf("release list = %d\n%s", code, out.String())
	}
	if got := out.String(); strings.Contains(got, "parked") || !strings.Contains(got, "live") {
		t.Fatalf("lookup failure changed the list:\n%s", got)
	}
}

func TestProjectInfoShowsParkedServices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects/retail":
			project := api.ProjectResponse{Workloads: []api.ProjectWorkloadResponse{
				{Slug: "retail-ui", WorkloadName: "ui", Status: appStatusParked, DeploymentStatus: "live"},
				{Slug: "retail-cart", WorkloadName: "cart", Status: appStatusParked, DeploymentStatus: "live"},
				{Slug: "retail-orders", WorkloadName: "orders", Status: "active", DeploymentStatus: "live"},
			}}
			project.Slug = "retail"
			_ = json.NewEncoder(w).Encode(project)
		case "/v1/apps/retail-ui":
			_ = json.NewEncoder(w).Encode(livenessParkedApp("retail-ui", "a1", "d1"))
		case "/v1/apps/retail-cart":
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a2", Slug: "retail-cart", Status: appStatusParked})
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	out := captureParkedStdout(t)
	if code := cmdProjectsInfo([]string{"retail"}); code != 0 {
		t.Fatalf("project info = %d\n%s", code, out.String())
	}
	got := out.String()
	for _, want := range []string{
		"retail-ui      live (parked: liveness failed)",
		"retail-cart    live (parked)",
		"retail-orders  live                            -\n",
		"! retail-ui is parked",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("project info missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "! retail-cart") {
		t.Fatalf("plain park got a failure notice:\n%s", got)
	}
}
