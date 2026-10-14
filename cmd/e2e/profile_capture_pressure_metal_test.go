//go:build metal

package e2e_test

import (
	"context"
	"encoding/json"
	"io"
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

// pressureApp is a running Hobby-plan instance of the Go fixture with
// on-demand profiling enabled, ready to grow its live heap.
type pressureApp struct {
	h                *e2etest.Harness
	key, slug, appID string
	depID            string
	client           *http.Client
	url, host        string
}

func startPressureApp(t *testing.T, prefix string) pressureApp {
	t.Helper()
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping memory pressure capture acceptance")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	if os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_BUILDER_BASE_PATH unset; skipping memory pressure capture acceptance")
	}
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		t.SkipNow()
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	builderImg, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	e2etest.OverrideBuilderBase(t, registry.AddImage("onebox-faas/builder-base", builderImg))
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", "profile-pressure")
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.Start(t, pool, e2etest.DeployWake, "FAAS_PROFILING_ON_DEMAND=1")
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
	})
	key := h.SeedAccount(context.Background(), api.PlanHobby)
	slug := prefix + "-" + randHexSuffix()
	falsy := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: slug, Type: "app", RequireAuthn: &falsy}); got != http.StatusCreated {
		t.Fatalf("create app: status=%d", got)
	}
	appID := mustGetAppID(t, h, key, slug)
	image, _ := e2etest.HelloImage("library/"+slug, "pressure hello")
	ref := registry.AddImage("library/"+slug, image)
	body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/deployments", api.CreateDeploymentRequest{Image: ref})
	if status != http.StatusAccepted {
		t.Fatalf("create deployment: status=%d body=%s", status, body)
	}
	depID := parseImageDeployment(t, body)
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	if _, err := e2etest.WaitForDeploymentLive(ctx, t, pool, depID, 110*time.Second); err != nil {
		t.Fatalf("deployment did not reach live: %v", err)
	}
	app := pressureApp{h: h, key: key, slug: slug, appID: appID, depID: depID, client: h.HTTPClient(), url: gatewayAppURL(h, slug), host: slug + ".apps.test.example"}
	if b, s := doGetWithHost(t, app.client, app.url, app.host, 60*time.Second); s != http.StatusOK || strings.TrimSpace(string(b)) != "pressure hello" {
		t.Fatalf("first request: status=%d body=%q", s, b)
	}
	if _, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateRunning, 30*time.Second); err != nil {
		t.Fatalf("instance not running: %v", err)
	}
	return app
}

// growUntilAutomaticCapture grows the heap in small steps until the platform
// queues a memory-pressure capture. The watcher samples every 2s, so small
// steps keep the overshoot past the threshold well short of an OOM kill.
func (a pressureApp) growUntilAutomaticCapture(t *testing.T) *api.ProfileCapture {
	t.Helper()
	grownMB := ""
	for step := 0; step < 120; step++ {
		b, s := doGetWithHost(t, a.client, a.url+"grow?mb=4", a.host, 10*time.Second)
		if s != http.StatusOK {
			t.Fatalf("grow step %d: status=%d body=%q", step, s, b)
		}
		grownMB = strings.TrimSpace(string(b))
		time.Sleep(750 * time.Millisecond)
		for _, c := range a.captures(t) {
			if c.Trigger == api.ProfileCaptureTriggerMemoryPressure {
				t.Logf("automatic capture %s queued after %s MiB grown: %s", c.ID, grownMB, c.TriggerDetail)
				return &c
			}
		}
	}
	t.Fatalf("no automatic capture after growing the heap to %s MiB", grownMB)
	return nil
}

func (a pressureApp) captures(t *testing.T) []api.ProfileCapture {
	t.Helper()
	b, s := doReq(t, a.h, a.key, http.MethodGet, "/v1/apps/"+a.slug+"/profiles/captures", nil)
	if s != http.StatusOK {
		t.Fatalf("list captures: status=%d body=%s", s, b)
	}
	var list api.ProfileCaptureList
	if err := json.Unmarshal(b, &list); err != nil {
		t.Fatal(err)
	}
	return list.Captures
}

// tryGetWithHost is doGetWithHost for requests expected to fail: a transport
// error returns status 0 instead of failing the test.
func tryGetWithHost(client *http.Client, url, host string, timeout time.Duration) int {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0
	}
	req.Host = host
	resp, err := client.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// waitCapture polls one capture until done returns true.
func (a pressureApp) waitCapture(t *testing.T, c *api.ProfileCapture, timeout time.Duration, done func(*api.ProfileCapture) bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !done(c) {
		if time.Now().After(deadline) {
			t.Fatalf("capture %s stuck: %+v", c.ID, c)
		}
		time.Sleep(250 * time.Millisecond)
		b, s := doReq(t, a.h, a.key, http.MethodGet, "/v1/apps/"+a.slug+"/profiles/captures/"+c.ID, nil)
		if s != http.StatusOK {
			t.Fatalf("get capture: status=%d body=%s", s, b)
		}
		if err := json.Unmarshal(b, c); err != nil {
			t.Fatal(err)
		}
	}
}

// TestProfileCaptureMemoryPressureMetal grows the Go fixture's live heap in
// a real guest until guest-init reports memory pressure, then expects the
// platform to have captured the heap automatically (ADR-1155): guest-init →
// vmmd → schedd ReportMemoryPressure → queued capture → vmmd capture → apid.
func TestProfileCaptureMemoryPressureMetal(t *testing.T) {
	a := startPressureApp(t, "profmem")
	h, key, slug, client, url, host := a.h, a.key, a.slug, a.client, a.url, a.host
	auto := a.growUntilAutomaticCapture(t)
	a.waitCapture(t, auto, 90*time.Second, func(c *api.ProfileCapture) bool { return c.Done() })
	if auto.Status != api.ProfileCaptureReady || auto.Processes < 1 || auto.RequestedInstanceID == "" || len(auto.Kinds) != 1 || auto.Kinds[0] != "heap" {
		t.Fatalf("automatic capture = %+v; want a ready heap capture of the pressured instance", auto)
	}
	assertCaptureFunction(t, h, key, slug, auto.ID, "heap", "profileFixtureGrow")

	if _, s := doGetWithHost(t, client, url+"release", host, 10*time.Second); s != http.StatusNoContent {
		t.Fatalf("release: status=%d", s)
	}
	if _, s := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/park", nil); s != http.StatusNoContent {
		t.Fatalf("cleanup park: status=%d", s)
	}
}

// TestProfileCaptureOOMMetal drives the fixture from memory pressure into a
// real OOM kill while the automatic heap capture runs (ADR-1155). The fixture
// is a single-process app, so the guest's OOM killer kills it and guest-init
// restarts it in place; guest-init must end the capture with the last heap
// checkpoint, mark the reply as OOM-killed, and schedd must link the kill.
func TestProfileCaptureOOMMetal(t *testing.T) {
	a := startPressureApp(t, "profoom")
	auto := a.growUntilAutomaticCapture(t)
	a.waitCapture(t, auto, 30*time.Second, func(c *api.ProfileCapture) bool {
		return c.Status == api.ProfileCaptureCapturing || c.Done()
	})
	if auto.Done() {
		t.Fatalf("automatic capture finished before the OOM push: %+v", auto)
	}
	// Let at least one 2s checkpoint land, then exhaust the plan's memory.
	time.Sleep(2500 * time.Millisecond)
	killed := false
	for step := 0; step < 30 && !killed; step++ {
		// The request that crosses the limit dies with the process: a reset
		// connection or a gateway error both mean the kill happened.
		killed = tryGetWithHost(a.client, a.url+"grow?mb=64", a.host, 10*time.Second) != http.StatusOK
	}
	if !killed {
		t.Fatal("the fixture was never OOM-killed")
	}
	a.waitCapture(t, auto, 90*time.Second, func(c *api.ProfileCapture) bool { return c.Done() && c.OOMKilledAt != nil })
	if auto.Status != api.ProfileCaptureReady || !auto.Partial || auto.OOMPeakMB <= 0 || auto.OOMPlanMB != 256 ||
		auto.DeploymentID != a.depID || auto.Processes < 1 || !strings.Contains(auto.Reason, "OOM-killed") {
		t.Fatalf("OOM capture = %+v; want a ready, partial heap capture linked to the kill", auto)
	}
	t.Logf("capture %s: OOM-killed at %s (%d MB of %d MB), %d profile(s)", auto.ID, auto.OOMKilledAt, auto.OOMPeakMB, auto.OOMPlanMB, len(auto.Profiles))
	assertCaptureFunction(t, a.h, a.key, a.slug, auto.ID, "heap", "profileFixtureGrow")
}
