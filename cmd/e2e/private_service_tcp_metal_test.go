//go:build metal

// adr: 530 — private TCP addressing between services, end to end through
// the real stack: a caller guest resolves <service>.svc.gregale through the
// bridge resolver, dials the service address on its natural port, the netns
// admits it, the host DNATs it to gatewayd-internal's service TCP proxy, the
// proxy recovers the original destination, authorizes the caller, wakes the
// parked internal target, and forwards bytes over vmmd's ForwardTCPStream.
package e2e_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// NodeFixtureTCPDialer is an HTTP app whose /dial endpoint opens a raw TCP
// connection from inside the guest, sends a payload, and reports what came
// back together with the address the guest's resolver returned.
func NodeFixtureTCPDialer(t *testing.T) []byte {
	t.Helper()
	const pkgJSON = `{
  "name": "faas-fixture-node-tcp-dialer",
  "version": "1.0.0",
  "private": true,
  "engines": {"node": "22"},
  "scripts": {"start": "node index.js"},
  "dependencies": {}
}
`
	const indexJS = `const http = require('http');
const net = require('net');
const dns = require('dns');
http.createServer((req, res) => {
  const u = new URL(req.url, 'http://fixture');
  if (u.pathname !== '/dial') { res.writeHead(200); res.end('dialer-ok'); return; }
  const host = u.searchParams.get('host');
  const port = parseInt(u.searchParams.get('port'), 10);
  const payload = u.searchParams.get('payload') || 'ping';
  dns.lookup(host, { family: 4 }, (lookupErr, address) => {
    const resolved = lookupErr ? '' : address;
    const target = lookupErr ? host : address;
    let reply = Buffer.alloc(0);
    let done = false;
    const sock = net.connect({ host: target, port });
    const finish = (status, body) => {
      if (done) return;
      done = true;
      sock.destroy();
      res.writeHead(status, { 'content-type': 'application/json' });
      res.end(JSON.stringify(Object.assign({ resolved }, body)));
    };
    sock.setTimeout(60000, () => finish(504, { error: 'timeout', reply: reply.toString() }));
    sock.on('connect', () => sock.write(payload));
    sock.on('data', (chunk) => {
      reply = Buffer.concat([reply, chunk]);
      if (reply.length >= payload.length + 'tcp-echo:'.length) finish(200, { reply: reply.toString() });
    });
    sock.on('error', (err) => finish(502, { error: err.code || err.message, reply: reply.toString() }));
    sock.on('end', () => finish(502, { error: 'closed', reply: reply.toString() }));
  });
}).listen(8080, '0.0.0.0', () => console.log('node fixture TCP dialer listening on :8080'));
`
	return buildTarGz(t, map[string]string{
		"package.json":     pkgJSON,
		"index.js":         indexJS,
		".faas-fixture":    "node22\n",
		"faas-build-token": time.Now().UTC().Format(time.RFC3339Nano) + "\n",
	})
}

type privateTCPDial struct {
	Resolved string `json:"resolved"`
	Reply    string `json:"reply"`
	Error    string `json:"error"`
	status   int
}

// privateTCPDialFrom asks a caller app to dial host:port from inside its guest.
func privateTCPDialFrom(t *testing.T, h *e2etest.Harness, callerSlug, host string, port int, payload string) privateTCPDial {
	t.Helper()
	query := url.Values{"host": {host}, "port": {strconv.Itoa(port)}, "payload": {payload}}
	req, err := http.NewRequest(http.MethodGet, h.GatewayURL+"/dial?"+query.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = callerSlug + ".apps.test.example"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	resp, err := h.HTTPClient().Do(req.WithContext(ctx))
	if err != nil {
		t.Fatalf("dial request via %s: %v", callerSlug, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	var out privateTCPDial
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("dial response from %s (status %d) is not JSON: %s", callerSlug, resp.StatusCode, body)
	}
	out.status = resp.StatusCode
	return out
}

func deployPrivateTCPApp(t *testing.T, ctx context.Context, h *e2etest.Harness, pool *pgxpool.Pool, key string, create api.CreateAppRequest, source []byte, port int) state.App {
	t.Helper()
	falsy := false
	create.Type, create.RequireAuthn = "app", &falsy
	if got := postOK(t, h, key, "/v1/apps", create); got != http.StatusCreated {
		t.Fatalf("create app %q: status=%d", create.Slug, got)
	}
	raw, status := postMultipartDeploymentWithOverrides(t, h, key, create.Slug, source, false, &api.CreateDeploymentOverrides{Port: port}, "")
	if status != http.StatusAccepted {
		t.Fatalf("deploy %s: status=%d body=%s", create.Slug, status, raw)
	}
	depID, _ := parseQueuedDeployment(t, raw)
	if _, _, err := e2etest.WaitForSourceDeployment(ctx, t, pool, depID, e2etest.DefaultBuildStallWindow, e2etest.DefaultBuildCeiling); err != nil {
		t.Fatalf("deployment of %s did not reach live: %v", create.Slug, err)
	}
	app, err := state.NewPgStore(pool).AppBySlug(ctx, create.Slug)
	if err != nil {
		t.Fatalf("load app %s: %v", create.Slug, err)
	}
	return app
}

func TestPrivateServiceTCPMetal(t *testing.T) {
	if !metalAvailable(t) {
		return
	}
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Turns on the gatewayd-internal service TCP listener and service-address
	// DNS answers on the bridge (pkg/e2etest gatewaydConfig).
	t.Setenv("FAAS_E2E_SERVICE_TCP", "1")
	h := e2etest.Start(t, pool, e2etest.DeployWake|e2etest.Builderd, "FAAS_SERVICE_TCP_ENABLED=1")
	defer h.DumpLogs(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*sourceDeployCtxTimeout())
	defer cancel()
	store := state.NewPgStore(pool)
	// The harness vmmd runs without a node name, so it does not register and
	// stamp a node of its own; stamp the seeded single-box node the way vmmd
	// does at boot, before any instance of this test exists.
	node, err := store.ComputeNodeByName(ctx, "default-local")
	if err != nil {
		t.Fatalf("load default-local node: %v", err)
	}
	if _, err := store.SetComputeNodeServiceAddressReady(ctx, node.ID, true); err != nil {
		t.Fatalf("stamp service address readiness: %v", err)
	}

	key := h.SeedAccount(ctx, api.PlanPro)
	suffix := randHexSuffix()
	cache := deployPrivateTCPApp(t, ctx, h, pool, key, api.CreateAppRequest{
		Slug:       "cache-" + suffix,
		Visibility: string(api.AppVisibilityInternal),
		Ports:      []api.WorkloadPort{{Port: 5432, Protocol: api.WorkloadPortTCP, Internal: true}},
	}, NodeFixtureTCP(t), 5432)
	caller := deployPrivateTCPApp(t, ctx, h, pool, key, api.CreateAppRequest{Slug: "api-" + suffix}, NodeFixtureTCPDialer(t), 8080)
	want, ok := api.ServiceAddressForIndex(cache.ServiceAddressIndex)
	if !ok {
		t.Fatalf("internal target %s has no service address (index %d)", cache.Slug, cache.ServiceAddressIndex)
	}
	if _, err := e2etest.WaitForInstanceState(ctx, t, pool, cache.ID, state.StateParked, 3*time.Minute); err != nil {
		t.Fatalf("target never parked: %v", err)
	}

	// Natural port, by name, to a parked internal target: DNS hands the
	// caller the service address, and the connection wakes the target.
	got := privateTCPDialFrom(t, h, caller.Slug, cache.Slug+".svc.gregale", 5432, "hello-private-tcp")
	if got.status != http.StatusOK || got.Reply != "tcp-echo:hello-private-tcp" {
		t.Fatalf("private TCP to %s.svc.gregale:5432 = %+v, want the echoed payload", cache.Slug, got)
	}
	if got.Resolved != want.String() {
		t.Fatalf("%s.svc.gregale resolved to %q, want the service address %s", cache.Slug, got.Resolved, want)
	}
	tcpMetalWaitMetric(t, ctx, h.GatewayControlURL, `gatewayd_internal_service_tcp_sessions_completed_total{outcome="success"}`, 1)

	// An undeclared port is refused before any byte reaches the guest.
	if undeclared := privateTCPDialFrom(t, h, caller.Slug, cache.Slug+".svc.gregale", 6000, "nope"); undeclared.status == http.StatusOK {
		t.Fatalf("undeclared port 6000 was forwarded: %+v", undeclared)
	}
	tcpMetalWaitMetric(t, ctx, h.GatewayControlURL, `gatewayd_internal_service_tcp_sessions_rejected_total{reason="undeclared_port"}`, 1)

	// The internal target has no public route.
	publicReq, err := http.NewRequest(http.MethodGet, gatewayAppURL(h, cache.Slug), nil)
	if err != nil {
		t.Fatal(err)
	}
	publicReq.Host = cache.Slug + ".apps.test.example"
	if resp, err := h.HTTPClient().Do(publicReq); err == nil {
		_ = resp.Body.Close()
		if resp.StatusCode < 400 {
			t.Fatalf("internal target answered on the public edge: status %d", resp.StatusCode)
		}
	}

	// Another account cannot use the name or the address.
	otherKey := h.SeedAccount(ctx, api.PlanPro)
	stranger := deployPrivateTCPApp(t, ctx, h, pool, otherKey, api.CreateAppRequest{Slug: "stranger-" + suffix}, NodeFixtureTCPDialer(t), 8080)
	byName := privateTCPDialFrom(t, h, stranger.Slug, cache.Slug+".svc.gregale", 5432, "leak")
	if byName.status == http.StatusOK || byName.Resolved == want.String() || strings.Contains(byName.Reply, "leak") {
		t.Fatalf("another account reached %s by name: %+v", cache.Slug, byName)
	}
	byAddress := privateTCPDialFrom(t, h, stranger.Slug, want.String(), 5432, "leak")
	if byAddress.status == http.StatusOK || strings.Contains(byAddress.Reply, "leak") {
		t.Fatalf("another account reached %s at %s: %+v", cache.Slug, want, byAddress)
	}
}
