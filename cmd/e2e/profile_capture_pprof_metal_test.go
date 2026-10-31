//go:build metal

package e2e_test

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// profilePprofPort is where the fixture serves net/http/pprof, apart from
// its application port.
const profilePprofPort = 6060

// TestProfileCapturePprofEndpointMetal profiles a Go application that runs no
// Gregale collector and only serves net/http/pprof (profiling.pprof_port,
// ADR-1155): guest-init collects CPU, heap and goroutine stacks from the
// endpoint for an on-demand capture, and again after a park and wake.
func TestProfileCapturePprofEndpointMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping pprof endpoint capture acceptance")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	if os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_BUILDER_BASE_PATH unset; skipping pprof endpoint capture acceptance")
	}
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	builderImg, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	e2etest.OverrideBuilderBase(t, registry.AddImage("onebox-faas/builder-base", builderImg))
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", "profile-pprof")
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.Start(t, pool, e2etest.DeployWake, "FAAS_PROFILING_ON_DEMAND=1")
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
	})
	key := h.SeedAccount(context.Background(), api.PlanHobby)
	slug := "profpprof-" + randHexSuffix()
	falsy := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: slug, Type: "app", RequireAuthn: &falsy}); got != http.StatusCreated {
		t.Fatalf("create app: status=%d", got)
	}
	appID := mustGetAppID(t, h, key, slug)
	if b, s := doReq(t, h, key, http.MethodPatch, "/v1/apps/"+slug, api.UpdateAppRequest{Profiling: &api.ProfilingConfig{PprofPort: profilePprofPort}}); s != http.StatusOK {
		t.Fatalf("set profiling.pprof_port: status=%d body=%s", s, b)
	}
	if b, s := doReq(t, h, key, http.MethodPatch, "/v1/apps/"+slug, api.UpdateAppRequest{Profiling: &api.ProfilingConfig{PprofPort: 9191}}); s < 400 || s >= 500 {
		t.Fatalf("reserved pprof_port: status=%d body=%s; want a client error", s, b)
	}
	image, _ := e2etest.HelloPprofImage("library/"+slug, "pprof hello", profilePprofPort)
	ref := registry.AddImage("library/"+slug, image)
	body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/deployments", api.CreateDeploymentRequest{Image: ref})
	if status != http.StatusAccepted {
		t.Fatalf("create deployment: status=%d body=%s", status, body)
	}
	depID := parseImageDeployment(t, body)
	deployCtx, deployCancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer deployCancel()
	if _, err := e2etest.WaitForDeploymentLive(deployCtx, t, pool, depID, 4*time.Minute); err != nil {
		t.Fatalf("deployment did not reach live: %v", err)
	}
	client, url, host := h.HTTPClient(), gatewayAppURL(h, slug), slug+".apps.test.example"
	if b, s := doGetWithHost(t, client, url, host, 60*time.Second); s != http.StatusOK || strings.TrimSpace(string(b)) != "pprof hello" {
		t.Fatalf("first request: status=%d body=%q", s, b)
	}
	if _, err := e2etest.WaitForInstanceState(deployCtx, t, pool, appID, state.StateRunning, 30*time.Second); err != nil {
		t.Fatalf("instance not running: %v", err)
	}

	// A request held open puts a known frame on a goroutine stack. It ends
	// before the gateway's 10 s slow-request report, which would queue an
	// automatic capture of its own.
	hung := make(chan struct{})
	go func() { defer close(hung); _ = tryGetWithHost(client, url+"hang?s=8", host, 20*time.Second) }()
	t.Cleanup(func() { <-hung })
	time.Sleep(500 * time.Millisecond)
	stopLoad := profileCaptureLoad(t, client, url+"work?ms=40&retain=4", host)
	capture := runProfileCapture(t, h, key, slug, api.CreateProfileCaptureRequest{Kinds: []string{"cpu", "heap", "stacks"}, DurationSeconds: 4}, false)
	stopLoad()
	if capture.Status != api.ProfileCaptureReady || capture.Processes != 1 || capture.Reason != "" {
		t.Fatalf("capture = %+v; want ready from the one scraped process", capture)
	}
	kinds := map[string]bool{}
	for _, p := range capture.Profiles {
		kinds[p.Kind] = true
	}
	if !kinds["cpu"] || !kinds["heap"] || !kinds["stacks"] {
		t.Fatalf("capture profiles = %+v; want cpu, heap and stacks", capture.Profiles)
	}
	assertCaptureFunction(t, h, key, slug, capture.ID, "cpu", "profileFixtureBurnCPU")
	assertCaptureFunction(t, h, key, slug, capture.ID, "heap", "profileFixtureRetain")
	assertCaptureFunction(t, h, key, slug, capture.ID, "stacks", "profileFixtureHang")
	// A request still in flight at park time wakes a second instance, which
	// the plan's concurrency of one then refuses for the wake below.
	<-hung
	assertContentionCapture(t, h, key, slug, client, url, host)

	// The scraper must not hold parking, and must work after restore.
	if _, s := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/park", nil); s != http.StatusNoContent {
		t.Fatalf("park: status=%d", s)
	}
	parkCtx, parkCancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer parkCancel()
	if _, err := e2etest.WaitForInstanceState(parkCtx, t, pool, appID, state.StateParked, 80*time.Second); err != nil {
		t.Fatalf("park with the pprof scraper: %v", err)
	}
	if b, s := doGetWithHost(t, client, url, host, 60*time.Second); s != http.StatusOK || strings.TrimSpace(string(b)) != "pprof hello" {
		t.Fatalf("wake: status=%d body=%q", s, b)
	}
	stopLoad = profileCaptureLoad(t, client, url+"work?ms=40&retain=2", host)
	restored := runProfileCapture(t, h, key, slug, api.CreateProfileCaptureRequest{Kinds: []string{"cpu"}, DurationSeconds: 3}, false)
	stopLoad()
	if restored.Status != api.ProfileCaptureReady || restored.Processes != 1 || len(restored.Profiles) == 0 {
		t.Fatalf("post-wake capture = %+v; want ready with a CPU profile", restored)
	}
	assertCaptureFunction(t, h, key, slug, restored.ID, "cpu", "profileFixtureBurnCPU")
	t.Logf("pprof endpoint captures ok: first=%s restored=%s", capture.ID, restored.ID)

	if _, s := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/park", nil); s != http.StatusNoContent {
		t.Fatalf("cleanup park: status=%d", s)
	}
}
