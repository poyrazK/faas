// adr: 568
package gateway

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// Internal listeners must never become public --port-<name> selectors.
func TestPublicPortsFromWorkloadPortsSkipsInternal(t *testing.T) {
	got := PublicPortsFromWorkloadPorts([]api.WorkloadPort{
		{Name: "metrics", Port: 9100, Protocol: api.WorkloadPortTCP},
		{Port: 6379, Protocol: api.WorkloadPortTCP, Internal: true},
	})
	if len(got) != 1 || got[0].Port != 9100 {
		t.Fatalf("public ports = %+v, want only the public listener", got)
	}
	if got := PublicPortsFromWorkloadPorts([]api.WorkloadPort{{Port: 6379, Protocol: api.WorkloadPortTCP, Internal: true}}); got == nil || len(got) != 0 {
		t.Fatalf("all-internal declaration = %#v, want an empty non-nil roster", got)
	}
	if got := PublicPortsFromWorkloadPorts(nil); got != nil {
		t.Fatalf("nil declaration = %#v, want nil", got)
	}
}
