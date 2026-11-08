package e2etest

import (
	"net"
	"strconv"
	"testing"

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
