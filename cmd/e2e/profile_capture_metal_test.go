//go:build metal

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestProfileCaptureMetal drives an on-demand capture (ADR-967) through the
// real stack: apid queues it, schedd claims it, vmmd delivers it to
// guest-init in a Firecracker guest, the Go fixture's dormant collector
// profiles CPU and heap, and apid merges the result. It then parks and
// restores the instance and captures again, proving dormant collectors
// survive the snapshot without the before_checkpoint handshake.
func TestProfileCaptureMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping profile capture acceptance")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	if os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_BUILDER_BASE_PATH unset; skipping profile capture acceptance")
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
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", "profile-capture")
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.Start(t, pool, e2etest.DeployWake, "FAAS_PROFILING_ON_DEMAND=1")
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
	})
	key := h.SeedAccount(context.Background(), api.PlanHobby)
	slug := "profcap-" + randHexSuffix()
	falsy := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: slug, Type: "app", RequireAuthn: &falsy}); got != http.StatusCreated {
		t.Fatalf("create app: status=%d", got)
	}
	appID := mustGetAppID(t, h, key, slug)
	image, _ := e2etest.HelloImage("library/"+slug, "profiled hello")
	ref := registry.AddImage("library/"+slug, image)
	body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/deployments", api.CreateDeploymentRequest{Image: ref})
	if status != http.StatusAccepted {
		t.Fatalf("create deployment: status=%d body=%s", status, body)
	}
	depID := parseImageDeployment(t, body)
	deployCtx, deployCancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer deployCancel()
	if _, err := e2etest.WaitForDeploymentLive(deployCtx, t, pool, depID, 110*time.Second); err != nil {
		t.Fatalf("deployment did not reach live: %v", err)
	}

	client, url, host := h.HTTPClient(), gatewayAppURL(h, slug), slug+".apps.test.example"
	if b, s := doGetWithHost(t, client, url, host, 60*time.Second); s != http.StatusOK || strings.TrimSpace(string(b)) != "profiled hello" {
		t.Fatalf("first request: status=%d body=%q", s, b)
	}
	if _, err := e2etest.WaitForInstanceState(deployCtx, t, pool, appID, state.StateRunning, 30*time.Second); err != nil {
		t.Fatalf("instance not running: %v", err)
	}

	stopLoad := profileCaptureLoad(t, client, url+"work?ms=40&retain=4", host)
	capture := runProfileCapture(t, h, key, slug, api.CreateProfileCaptureRequest{Kinds: []string{"cpu", "heap"}, DurationSeconds: 4}, true)
	stopLoad()
	if capture.Status != api.ProfileCaptureReady || capture.Processes < 1 || capture.InstanceID == "" {
		t.Fatalf("capture = %+v; want ready with an instrumented process", capture)
	}
	kinds := map[string]bool{}
	for _, p := range capture.Profiles {
		kinds[p.Kind] = true
	}
	if !kinds["cpu"] || !kinds["heap"] {
		t.Fatalf("capture profiles = %+v; want cpu and heap", capture.Profiles)
	}
	assertCaptureFunction(t, h, key, slug, capture.ID, "cpu", "profileFixtureBurnCPU")
	assertCaptureFunction(t, h, key, slug, capture.ID, "heap", "profileFixtureRetain")
	if raw, s := doReq(t, h, key, http.MethodGet, "/v1/apps/"+slug+"/profiles/captures/"+capture.ID+"/pprof?kind=heap", nil); s != http.StatusOK || len(raw) < 2 || raw[0] != 0x1f || raw[1] != 0x8b {
		t.Fatalf("pprof download: status=%d bytes=%d", s, len(raw))
	}

	// Dormant collectors must not block parking, and must work after restore.
	if _, s := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/park", nil); s != http.StatusNoContent {
		t.Fatalf("park: status=%d", s)
	}
	parkCtx, parkCancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer parkCancel()
	if _, err := e2etest.WaitForInstanceState(parkCtx, t, pool, appID, state.StateParked, 80*time.Second); err != nil {
		t.Fatalf("park with dormant collectors: %v", err)
	}
	parked := runProfileCapture(t, h, key, slug, api.CreateProfileCaptureRequest{Kinds: []string{"cpu"}, DurationSeconds: 1}, false)
	if parked.Status != api.ProfileCaptureFailed || !strings.Contains(parked.Reason, "no running instance") {
		t.Fatalf("parked capture = %+v; want failed with a no-running-instance reason", parked)
	}
	if b, s := doGetWithHost(t, client, url, host, 60*time.Second); s != http.StatusOK || strings.TrimSpace(string(b)) != "profiled hello" {
		t.Fatalf("wake: status=%d body=%q", s, b)
	}
	stopLoad = profileCaptureLoad(t, client, url+"work?ms=40&retain=2", host)
	restored := runProfileCapture(t, h, key, slug, api.CreateProfileCaptureRequest{Kinds: []string{"cpu"}, DurationSeconds: 3}, false)
	stopLoad()
	if restored.Status != api.ProfileCaptureReady || restored.Processes < 1 || len(restored.Profiles) == 0 {
		t.Fatalf("post-wake capture = %+v; want ready with a CPU profile", restored)
	}
	assertCaptureFunction(t, h, key, slug, restored.ID, "cpu", "profileFixtureBurnCPU")
	t.Logf("captures ok: first=%s (instance %s, %d processes) restored=%s", capture.ID, capture.InstanceID, capture.Processes, restored.ID)

	if _, s := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/park", nil); s != http.StatusNoContent {
		t.Fatalf("cleanup park: status=%d", s)
	}
}

// runProfileCapture queues a capture and polls it to a terminal status.
// checkSingle also asserts a second concurrent capture is refused.
func runProfileCapture(t *testing.T, h *e2etest.Harness, key, slug string, req api.CreateProfileCaptureRequest, checkSingle bool) api.ProfileCapture {
	t.Helper()
	path := "/v1/apps/" + slug + "/profiles/captures"
	body, status := doReq(t, h, key, http.MethodPost, path, req)
	if status != http.StatusAccepted {
		t.Fatalf("create capture: status=%d body=%s", status, body)
	}
	var capture api.ProfileCapture
	if err := json.Unmarshal(body, &capture); err != nil {
		t.Fatal(err)
	}
	if checkSingle {
		if b, s := doReq(t, h, key, http.MethodPost, path, req); s != http.StatusConflict {
			t.Fatalf("concurrent capture: status=%d body=%s; want 409", s, b)
		}
	}
	deadline := time.Now().Add(time.Duration(req.DurationSeconds)*time.Second + 90*time.Second)
	for !capture.Done() {
		if time.Now().After(deadline) {
			t.Fatalf("capture %s stuck in %s", capture.ID, capture.Status)
		}
		time.Sleep(500 * time.Millisecond)
		b, s := doReq(t, h, key, http.MethodGet, path+"/"+capture.ID, nil)
		if s != http.StatusOK {
			t.Fatalf("get capture: status=%d body=%s", s, b)
		}
		if err := json.Unmarshal(b, &capture); err != nil {
			t.Fatal(err)
		}
	}
	return capture
}

func assertCaptureFunction(t *testing.T, h *e2etest.Harness, key, slug, id, kind, want string) {
	t.Helper()
	body, status := doReq(t, h, key, http.MethodGet, "/v1/apps/"+slug+"/profiles/captures/"+id+"/view?kind="+kind, nil)
	if status != http.StatusOK {
		t.Fatalf("%s view: status=%d body=%s", kind, status, body)
	}
	var view api.ProfileCaptureView
	if err := json.Unmarshal(body, &view); err != nil {
		t.Fatal(err)
	}
	for _, f := range view.Functions {
		if strings.Contains(f.Name, want) && f.Total > 0 {
			t.Logf("%s capture: %s total=%d %s, %s total=%d", kind, id, view.Total, view.Unit, f.Name, f.Total)
			return
		}
	}
	top := []string{}
	for i, f := range view.Functions {
		if i == 8 {
			break
		}
		top = append(top, f.Name)
	}
	t.Fatalf("%s view lacks %q (total=%d empty=%t top=%v)", kind, want, view.Total, view.Empty, top)
}

// profileCaptureLoad sends sequential /work requests until stopped.
func profileCaptureLoad(t *testing.T, client *http.Client, url, host string) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for ctx.Err() == nil {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				return
			}
			req.Host = host
			if resp, err := client.Do(req); err == nil {
				_ = resp.Body.Close()
			}
		}
	}()
	return func() { cancel(); wg.Wait() }
}
