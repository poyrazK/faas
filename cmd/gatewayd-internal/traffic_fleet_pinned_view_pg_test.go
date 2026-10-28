//go:build !no_pg

// adr: 570
package main

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-20261028095914: a pinned request, here an X-Gregale-Revision pin,
// admits from the gateway's routing view while the control-plane Postgres is
// unreachable, until the outage grace, instead of reading the store per
// request. Budget admission and central counters are off, as in the
// ordinary-request outage test.
func TestTrafficFleetDaemonServesRevisionPinDuringPostgresOutage(t *testing.T) {
	f := newFleetDaemonFixture(t)
	config := f.pool.Config().ConnConfig
	proxy := startCuttablePostgresProxy(t, net.JoinHostPort(config.Host, strconv.Itoa(int(config.Port))))
	const grace = 4 * time.Second
	daemon := startFleetDaemonConfigured(t, f.pool, f.apps, f.nodes[0].ID, f.nodes[0].Name, f.usage, fleetDaemonOptions{
		LocalCounters: true, DatabaseAddr: proxy.listener.Addr().String(), RoutingGraceMS: int(grace / time.Millisecond),
		LogWarnings: true, CachedHostPolicy: true, BudgetAdmissionOff: true})
	t.Cleanup(func() {
		if t.Failed() {
			daemon.stop(t)
			t.Logf("daemon stderr:\n%s", &daemon.stderr)
		}
	})
	app := f.apps[2]
	pin := http.Header{api.RevisionHeader: {app.Deployment}}
	call := func() (fleetDaemonResponse, error) {
		return requestFleetDaemonHeader(t.Context(), daemon.ready.Endpoint, app.Host, "/pinned", pin)
	}
	for range 2 {
		if response, err := call(); err != nil || response.status != http.StatusOK || response.body != "guest served" {
			t.Fatalf("revision pin before the outage: %+v err=%v", response, err)
		}
		// Let one refresh land, so the outage starts from a refreshed view.
		time.Sleep(1500 * time.Millisecond)
	}
	proxy.setCut(true)
	cut := time.Now()
	served := 0
	var refused fleetDaemonResponse
	var refusedAt time.Time
	for time.Since(cut) < grace+5*time.Second {
		response, err := call()
		if err != nil {
			t.Fatal(err)
		}
		if response.status != http.StatusOK {
			refused, refusedAt = response, time.Now()
			break
		}
		served++
		time.Sleep(200 * time.Millisecond)
	}
	if refusedAt.IsZero() {
		t.Fatalf("the routing view admitted %d pinned requests past its grace", served)
	}
	if elapsed := refusedAt.Sub(cut); elapsed < grace-1500*time.Millisecond || served < 5 {
		t.Fatalf("refused %s after the cut with %d served (status=%d body=%q); want service until the %s grace",
			elapsed, served, refused.status, refused.body, grace)
	}
	if refused.status != http.StatusServiceUnavailable || !strings.Contains(refused.body, "traffic_policy_unavailable") {
		t.Fatalf("past the grace: status=%d body=%q; want 503 traffic_policy_unavailable", refused.status, refused.body)
	}
	t.Logf("served %d revision-pinned requests for %s with Postgres unreachable, refused after", served, refusedAt.Sub(cut).Round(time.Millisecond))
	proxy.setCut(false)
	for until := time.Now().Add(10 * time.Second); ; {
		response, err := call()
		if err == nil && response.status == http.StatusOK {
			break
		}
		if time.Now().After(until) {
			t.Fatalf("no recovery after Postgres returned: %+v err=%v", response, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
