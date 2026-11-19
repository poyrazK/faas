package fcvm

import (
	"net/netip"
	"strings"
	"testing"
)

// A2: a Compose service's bare name resolves through svc.gregale.
func TestServiceDiscoveryResolverSearchesServiceDomain(t *testing.T) {
	got := string(serviceDiscoveryResolverContents(netip.MustParseAddr("10.0.0.1")))
	for _, want := range []string{"nameserver 10.0.0.1\n", "search svc.gregale\n", "ndots:1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("resolver %q lacks %q", got, want)
		}
	}
}
