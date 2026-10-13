//go:build metal

// adr: 576 — a build-free private TCP fixture: an internal TCP echo service
// and a caller app, both deployed from images, so TCP tests do not depend on
// builderd or the host's build-drive headroom.
package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
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

	// Turns on the service TCP listener and service-address DNS answers.
	t.Setenv("FAAS_E2E_SERVICE_TCP", "1")
	h := e2etest.Start(t, pool, e2etest.DeployWake, "FAAS_SERVICE_TCP_ENABLED=1")
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
	return f
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
// address from inside its guest and send payload.
func (f *privateTCPImageFixture) dial(t *testing.T, payload string) privateTCPDial {
	t.Helper()
	return privateTCPDialFrom(t, f.h, f.key, f.caller.Slug, f.address, privateTCPEchoPort, payload)
}

func (f *privateTCPImageFixture) echoed(got privateTCPDial, payload string) bool {
	return got.status == http.StatusOK && got.Reply == "tcp-echo:"+payload
}

// park parks app and waits until no instance of it is left running.
func (f *privateTCPImageFixture) park(t *testing.T, app state.App) {
	t.Helper()
	if raw, status := doReq(t, f.h, f.key, http.MethodPost, "/v1/apps/"+app.Slug+"/park", nil); status/100 != 2 {
		t.Fatalf("park %s: status=%d body=%s", app.Slug, status, raw)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if _, err := e2etest.WaitForAppParked(ctx, t, f.pool, app.ID, 40*time.Second); err != nil {
		t.Fatalf("%s not parked: %v", app.Slug, err)
	}
}
