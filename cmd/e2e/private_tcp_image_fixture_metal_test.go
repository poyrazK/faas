//go:build metal

// adr: 576 — a build-free private TCP fixture: an internal TCP echo service
// and a caller app, both deployed from images, so TCP tests do not depend on
// a source build or the host's build-drive headroom.
package e2e_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// privateTCPEchoPort is the internal listener the echo service declares.
const privateTCPEchoPort = 5432

// privateTCPDialTimeout bounds one dial through the caller. A restore that
// fails falls back to a cold boot (ADR-005), which on a busy nested-KVM host
// takes longer than the harness client's 30 s; that is slow, not wrong.
const privateTCPDialTimeout = 2 * time.Minute

type privateTCPImageFixture struct {
	h       *e2etest.Harness
	pool    *pgxpool.Pool
	key     string
	caller  state.App // dials through GET /dial
	service state.App // internal, TCP echo on privateTCPEchoPort
	address string    // the service's private address
}

func newPrivateTCPImageFixture(t *testing.T) *privateTCPImageFixture {
	t.Helper()
	if !metalAvailable(t) {
		return nil
	}
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return nil
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	builderImg, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	e2etest.OverrideBuilderBase(t, registry.AddImage("onebox-faas/builder-base", builderImg))
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", helloBody)
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")

	// Turns on the service TCP listener and service-address DNS answers. The
	// harness wires the guest-facing service listeners only when builderd
	// runs; these image deploys never ask it for a build drive.
	t.Setenv("FAAS_E2E_SERVICE_TCP", "1")
	h := e2etest.Start(t, pool, e2etest.DeployWake|e2etest.Builderd, "FAAS_SERVICE_TCP_ENABLED=1")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	store := state.NewPgStore(pool)
	// The harness vmmd does not register a node of its own; stamp the seeded
	// single-box node the way vmmd does at boot.
	node, err := store.ComputeNodeByName(ctx, "default-local")
	if err != nil {
		t.Fatalf("load default-local node: %v", err)
	}
	if _, err := store.SetComputeNodeServiceAddressReady(ctx, node.ID, true); err != nil {
		t.Fatalf("stamp service address readiness: %v", err)
	}

	f := &privateTCPImageFixture{h: h, pool: pool, key: h.SeedAccount(ctx, api.PlanPro)}
	suffix := randHexSuffix()
	echoImg, _ := e2etest.TCPEchoImageAboveBase("library/tcp-echo", helloBody, privateTCPEchoPort)
	f.service = f.deploy(t, ctx, api.CreateAppRequest{
		Slug:       "cache-" + suffix,
		Visibility: string(api.AppVisibilityInternal),
		Ports:      []api.WorkloadPort{{Port: privateTCPEchoPort, Protocol: api.WorkloadPortTCP, Internal: true}},
	}, registry.AddImage("library/tcp-echo", echoImg))
	callerImg, _ := e2etest.HelloImageAboveBase("library/hello", helloBody)
	f.caller = f.deploy(t, ctx, api.CreateAppRequest{Slug: "api-" + suffix}, registry.AddImage("library/hello", callerImg))

	index, err := store.AppServiceAddressIndex(ctx, f.service.ID)
	if err != nil {
		t.Fatalf("service address index of %s: %v", f.service.Slug, err)
	}
	address, ok := api.ServiceAddressForIndex(index)
	if !ok {
		t.Fatalf("internal service %s has no service address (index %d)", f.service.Slug, index)
	}
	f.address = address.String()
	// Park both apps while the harness daemons still run: a VM left running
	// when the harness stops vmmd outlives the test, and leakcheck reports
	// its jail. Cleanups run last-in first-out, so this runs first.
	t.Cleanup(func() {
		for _, app := range []state.App{f.caller, f.service} {
			f.parkAtCleanup(t, app)
		}
	})
	return f
}

// parkAtCleanup is park without failing the test: a park that does not
// finish is logged, and run.sh's leakcheck still reports any VM left behind.
func (f *privateTCPImageFixture) parkAtCleanup(t *testing.T, app state.App) {
	t.Helper()
	if raw, status := doReq(t, f.h, f.key, http.MethodPost, "/v1/apps/"+app.Slug+"/park", nil); status/100 != 2 {
		t.Logf("cleanup: park %s: status=%d body=%s", app.Slug, status, raw)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if _, err := e2etest.WaitForAppParked(ctx, t, f.pool, app.ID, 40*time.Second); err != nil {
		t.Logf("cleanup: %s not parked: %v", app.Slug, err)
	}
}

func (f *privateTCPImageFixture) deploy(t *testing.T, ctx context.Context, create api.CreateAppRequest, image string) state.App {
	t.Helper()
	falsy := false
	create.Type, create.RequireAuthn = "app", &falsy
	if got := postOK(t, f.h, f.key, "/v1/apps", create); got != http.StatusCreated {
		t.Fatalf("create app %q: status=%d", create.Slug, got)
	}
	raw, status := doReq(t, f.h, f.key, http.MethodPost, "/v1/apps/"+create.Slug+"/deployments", api.CreateDeploymentRequest{Image: image})
	if status != http.StatusAccepted {
		t.Fatalf("deploy %s: status=%d body=%s", create.Slug, status, raw)
	}
	var dep api.DeploymentResponse
	if err := json.Unmarshal(raw, &dep); err != nil {
		t.Fatalf("decode deployment of %s: %v", create.Slug, err)
	}
	if _, err := e2etest.WaitForDeploymentLive(ctx, t, f.pool, dep.ID, 10*time.Minute); err != nil {
		f.h.DumpLogs(t)
		t.Fatalf("deployment of %s did not reach live: %v", create.Slug, err)
	}
	app, err := state.NewPgStore(f.pool).AppBySlug(ctx, create.Slug)
	if err != nil {
		t.Fatalf("load app %s: %v", create.Slug, err)
	}
	return app
}

// dial asks the caller to open a TCP session to the service's private
// address from inside its guest and send payload. A request that fails
// outright comes back with Error set and status 0.
func (f *privateTCPImageFixture) dial(t *testing.T, payload string) privateTCPDial {
	t.Helper()
	query := url.Values{"host": {f.address}, "port": {strconv.Itoa(privateTCPEchoPort)}, "payload": {payload}}
	req, err := http.NewRequest(http.MethodGet, f.h.GatewayURL+"/dial?"+query.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = f.caller.Slug + ".apps.test.example"
	req.Header.Set("Authorization", "Bearer "+f.key)
	resp, err := (&http.Client{Timeout: privateTCPDialTimeout}).Do(req)
	if err != nil {
		return privateTCPDial{Error: err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	var out privateTCPDial
	if err := json.Unmarshal(body, &out); err != nil {
		out.Error = "not the dialer's response: " + string(body)
	}
	out.status = resp.StatusCode
	return out
}

func (f *privateTCPImageFixture) echoed(got privateTCPDial, payload string) bool {
	return got.status == http.StatusOK && got.Reply == "tcp-echo:"+payload
}

// park parks app and waits until no instance of it is left running. A park
// whose drain misses its deadline on a busy host is retried once.
func (f *privateTCPImageFixture) park(t *testing.T, app state.App) {
	t.Helper()
	raw, status := doReq(t, f.h, f.key, http.MethodPost, "/v1/apps/"+app.Slug+"/park", nil)
	if status == http.StatusServiceUnavailable {
		t.Logf("park %s: status=%d body=%s; retrying once", app.Slug, status, raw)
		time.Sleep(5 * time.Second)
		raw, status = doReq(t, f.h, f.key, http.MethodPost, "/v1/apps/"+app.Slug+"/park", nil)
	}
	if status/100 != 2 {
		t.Fatalf("park %s: status=%d body=%s", app.Slug, status, raw)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if _, err := e2etest.WaitForAppParked(ctx, t, f.pool, app.ID, 40*time.Second); err != nil {
		t.Fatalf("%s not parked: %v", app.Slug, err)
	}
}
