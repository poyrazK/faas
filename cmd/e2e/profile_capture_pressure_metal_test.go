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

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// pressureApp is a running Hobby-plan instance of the Go fixture with
// on-demand profiling enabled, ready to grow its live heap.
type pressureApp struct {
	h                *e2etest.Harness
	pool             *pgxpool.Pool
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
	// Image deploys include the layer secret and abuse scans; give a loaded
	// host room before the capture under test starts.
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Minute)
	defer cancel()
	if _, err := e2etest.WaitForDeploymentLive(ctx, t, pool, depID, 4*time.Minute); err != nil {
		t.Fatalf("deployment did not reach live: %v", err)
	}
	app := pressureApp{h: h, pool: pool, key: key, slug: slug, appID: appID, depID: depID, client: h.HTTPClient(), url: gatewayAppURL(h, slug), host: slug + ".apps.test.example"}
	app.subscribeCaptureWebhook(t)
	// Park before the harness stops the daemons (cleanups run LIFO): an
	// instance still running at teardown leaks its VM, and an app that was
	// OOM-killed and restarted in place is still running.
	t.Cleanup(func() {
		if _, s := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/park", nil); s != http.StatusNoContent {
			t.Errorf("cleanup park: status=%d", s)
		}
	})
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

// subscribeCaptureWebhook registers an app webhook for the capture events
// (ADR-1155) straight in the store: the API would resolve the target and
// seal a secret, neither of which this test is about. Deliveries to the
// unreachable target fail; the events are asserted in the outbox and ledger.
func (a pressureApp) subscribeCaptureWebhook(t *testing.T) {
	t.Helper()
	var accountID string
	if err := a.pool.QueryRow(t.Context(), `SELECT account_id::text FROM apps WHERE id = $1::uuid`, a.appID).Scan(&accountID); err != nil {
		t.Fatalf("app account: %v", err)
	}
	if _, err := state.NewPgStore(a.pool).CreateAppWebhook(t.Context(), state.AppWebhook{AppID: a.appID, AccountID: accountID,
		TargetURL: "https://hooks.invalid/profile-captures", SecretSealed: []byte("unused"), Enabled: true,
		EventFilter: []string{string(state.AppWebhookEventProfileCaptureReady), string(state.AppWebhookEventProfileCaptureOOMLinked)}}); err != nil {
		t.Fatalf("create capture webhook: %v", err)
	}
}

// assertCaptureNotified expects exactly one profile.capture_ready event for
// the capture, naming the OOM kill when wantOOM, and no
// profile.capture_oom_linked: a kill during the capture is announced with it.
func (a pressureApp) assertCaptureNotified(t *testing.T, captureID, trigger string, wantOOM bool) {
	t.Helper()
	const q = `SELECT event, payload FROM app_webhook_event_outbox WHERE app_id = $1::text::uuid AND source_id = $2::text::uuid
		UNION ALL SELECT event, payload FROM app_webhook_deliveries WHERE app_id = $1::text::uuid AND payload->'capture'->>'id' = $2::text`
	var events []string
	var payload api.ProfileCaptureWebhookPayload
	deadline := time.Now().Add(15 * time.Second)
	for {
		events = events[:0]
		rows, err := a.pool.Query(t.Context(), q, a.appID, captureID)
		if err != nil {
			t.Fatalf("capture notifications: %v", err)
		}
		for rows.Next() {
			var event string
			var body []byte
			if err := rows.Scan(&event, &body); err != nil {
				t.Fatalf("scan capture notification: %v", err)
			}
			events = append(events, event)
			if event == string(state.AppWebhookEventProfileCaptureReady) {
				if err := json.Unmarshal(body, &payload); err != nil {
					t.Fatalf("decode capture notification: %v", err)
				}
			}
		}
		rows.Close()
		if len(events) > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if len(events) != 1 || events[0] != string(state.AppWebhookEventProfileCaptureReady) {
		t.Fatalf("capture %s notifications = %v; want one profile.capture_ready", captureID, events)
	}
	if payload.Capture.ID != captureID || payload.Capture.Trigger != trigger || (payload.Capture.OOMKilledAt != nil) != wantOOM ||
		payload.CapturePath != "/dashboard/apps/"+a.slug+"/profiles/captures/"+captureID {
		t.Fatalf("capture notification = %+v; want the capture (OOM=%v) and its page", payload, wantOOM)
	}
	t.Logf("capture %s announced: %s (OOM-killed: %v)", captureID, events[0], wantOOM)
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
	a.assertCaptureNotified(t, auto.ID, api.ProfileCaptureTriggerMemoryPressure, false)

	if _, s := doGetWithHost(t, client, url+"release", host, 10*time.Second); s != http.StatusNoContent {
		t.Fatalf("release: status=%d", s)
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
	a.assertCaptureNotified(t, auto.ID, api.ProfileCaptureTriggerMemoryPressure, true)
}

// TestProfileCaptureCPUSaturationMetal holds the fixture at its CPU limit and
// expects schedd's stats poller to queue an automatic CPU capture (ADR-1155):
// vmmd cgroup CPU stats → schedd poller → queued capture → vmmd capture.
func TestProfileCaptureCPUSaturationMetal(t *testing.T) {
	a := startPressureApp(t, "profcpu")
	if b, s := doGetWithHost(t, a.client, a.url+"spin?s=90&cpus=2", a.host, 10*time.Second); s != http.StatusAccepted {
		t.Fatalf("spin: status=%d body=%q", s, b)
	}
	var auto *api.ProfileCapture
	for deadline := time.Now().Add(api.ProfileCPUSaturationAfter + 40*time.Second); auto == nil && time.Now().Before(deadline); {
		time.Sleep(2 * time.Second)
		for _, c := range a.captures(t) {
			if c.Trigger == api.ProfileCaptureTriggerCPUSaturation {
				auto = &c
				break
			}
		}
	}
	if auto == nil {
		t.Fatal("no automatic CPU capture while the fixture held its CPU limit")
	}
	t.Logf("automatic capture %s queued: %s", auto.ID, auto.TriggerDetail)
	a.waitCapture(t, auto, 60*time.Second, func(c *api.ProfileCapture) bool { return c.Done() })
	if auto.Status != api.ProfileCaptureReady || len(auto.Kinds) != 1 || auto.Kinds[0] != api.ProfileKindCPU || !strings.Contains(auto.TriggerDetail, "CPU use at") {
		t.Fatalf("automatic capture = %+v; want a ready CPU capture of the saturated instance", auto)
	}
	assertCaptureFunction(t, a.h, a.key, a.slug, auto.ID, "cpu", "profileFixtureSpin")
	a.assertCaptureNotified(t, auto.ID, api.ProfileCaptureTriggerCPUSaturation, false)
}

// TestProfileCaptureSlowRequestMetal holds a request in the fixture past the
// gateway's slow-request threshold and expects an automatic stacks+CPU
// capture whose stacks show the goroutine the request is stuck in (ADR-1155):
// gatewayd-internal → schedd ReportSlowRequest → queued capture → vmmd.
func TestProfileCaptureSlowRequestMetal(t *testing.T) {
	a := startPressureApp(t, "profslow")
	hung := make(chan int, 1)
	go func() { hung <- tryGetWithHost(a.client, a.url+"hang?s=20", a.host, 40*time.Second) }()
	var auto *api.ProfileCapture
	deadline := time.Now().Add(api.ProfileCaptureSlowRequestAfter + 30*time.Second)
	for auto == nil && time.Now().Before(deadline) {
		time.Sleep(time.Second)
		for _, c := range a.captures(t) {
			if c.Trigger == api.ProfileCaptureTriggerSlowRequest {
				auto = &c
			}
		}
	}
	if auto == nil {
		t.Fatal("no automatic capture while a request hung past the slow-request threshold")
	}
	t.Logf("slow-request capture %s queued: %s", auto.ID, auto.TriggerDetail)
	a.waitCapture(t, auto, 60*time.Second, func(c *api.ProfileCapture) bool { return c.Done() })
	if auto.Status != api.ProfileCaptureReady || auto.Processes < 1 || auto.RequestID == "" || auto.RequestTraceID == "" ||
		len(auto.Kinds) != 2 || auto.Kinds[0] != api.ProfileKindStacks || auto.Kinds[1] != api.ProfileKindCPU {
		t.Fatalf("slow-request capture = %+v; want a ready stacks+cpu capture naming the request", auto)
	}
	assertCaptureFunction(t, a.h, a.key, a.slug, auto.ID, api.ProfileKindStacks, "profileFixtureHang")
	if status := <-hung; status != http.StatusOK {
		t.Fatalf("hung request finished with status %d", status)
	}
	// The request explorer links back to the snapshot once the request's
	// telemetry is recorded. Harnesses without request telemetry skip this.
	linkDeadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(linkDeadline) {
		b, s := doReq(t, a.h, a.key, http.MethodGet, "/v1/apps/"+a.slug+"/debug/requests/"+auto.RequestID, nil)
		if s == http.StatusOK {
			var item api.DebugTelemetryRequestItem
			if err := json.Unmarshal(b, &item); err != nil {
				t.Fatal(err)
			}
			if item.ProfileCaptureID == auto.ID {
				return
			}
		}
		time.Sleep(2 * time.Second)
	}
	t.Log("request telemetry did not link the capture within 30s; this harness may not record request telemetry")
}
