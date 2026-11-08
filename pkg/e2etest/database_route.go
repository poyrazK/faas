package e2etest

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// DatabaseRouteUpstreamEnv names the host:port the harness's gatewayd-internal
// forwards the managed-database route to (ADR-1027).
const DatabaseRouteUpstreamEnv = "FAAS_E2E_DATABASE_ROUTE_UPSTREAM"

// DatabaseRouteEnvironment lets guests reach a disposable SQL proxy the way
// production apps reach pgproxyd: they dial api.DatabaseRouteAddress on the
// PostgreSQL port, the host DNATs that onto gatewayd-internal's forwarder on
// the tenant bridge, and the forwarder relays to upstream (the proxy, on a
// host address such as loopback). The host firewall drops guest traffic to
// any other host-local address (spec §11), so a proxy bound beside the guests
// is unreachable without this route.
//
// Call it before Start and pass the returned variables to Start. Publish the
// database at DatabaseRouteEndpoint() so bindings carry the route address.
func DatabaseRouteEnvironment(t *testing.T, upstream string) []string {
	t.Helper()
	if _, _, err := net.SplitHostPort(upstream); err != nil {
		t.Fatalf("e2etest: database route upstream %q: %v", upstream, err)
	}
	t.Setenv(DatabaseRouteUpstreamEnv, upstream)
	return []string{"FAAS_VMMD_DATABASE_ROUTE_ENABLED=1"}
}

// DatabaseRouteEndpoint is the address guests dial for a routed database.
func DatabaseRouteEndpoint() (host string, port uint16) {
	return api.DatabaseRouteAddress().String(), api.DatabaseRoutePort
}

// DatabaseRouteHostPort is DatabaseRouteEndpoint as one host:port.
func DatabaseRouteHostPort() string {
	host, port := DatabaseRouteEndpoint()
	return net.JoinHostPort(host, strconv.Itoa(int(port)))
}

// ReserveLoopbackAddress picks a free 127.0.0.1 port for a SQL proxy that
// opens after Start (fixtures built in a deploy hook): the route's upstream
// must be named before gatewayd-internal starts. The port is released before
// returning; the proxy binds it moments later.
func ReserveLoopbackAddress(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("e2etest: reserve loopback address: %v", err)
	}
	address := ln.Addr().String()
	_ = ln.Close()
	return address
}

// LoopbackPort is the port of a ReserveLoopbackAddress result.
func LoopbackPort(t *testing.T, address string) uint16 {
	t.Helper()
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatalf("e2etest: loopback address %q: %v", address, err)
	}
	value, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		t.Fatalf("e2etest: loopback port %q: %v", port, err)
	}
	return uint16(value)
}

// WaitForBuilderBaseScan waits until imaged has rescanned the builder base
// after started. On startup imaged replaces the base's scan sidecar with a
// failed-scan placeholder and rescans (about a minute on n2); vmmd refuses to
// boot a builder on the placeholder ("Base image scan unavailable"), so a
// build queued in that window fails.
func WaitForBuilderBaseScan(t *testing.T, started time.Time, timeout time.Duration) {
	t.Helper()
	root := os.Getenv("FAAS_STORAGE_ROOT")
	if root == "" {
		root = "/srv/fc"
	}
	sidecar := filepath.Join(root, "scans", filepath.Base(StagedBuilderBasePath())+".scan.json")
	deadline := time.Now().Add(timeout)
	for {
		if info, err := os.Stat(sidecar); err == nil && !info.ModTime().Before(started) {
			if raw, err := os.ReadFile(sidecar); err == nil && !strings.Contains(string(raw), "0001-01-01T00:00:00Z") {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("e2etest: builder base scan %s not refreshed within %s", sidecar, timeout)
		}
		time.Sleep(2 * time.Second)
	}
}
