// adr: 025

package fcvm

import (
	"context"
	"net/netip"
	"strings"
	"testing"
)

func TestReconcilePrivateNetworkFabricIsIdempotent(t *testing.T) {
	run := &fakeRunner{}
	m := newTestManager(run, &fakeVMM{})
	cidr := netip.MustParsePrefix("10.42.0.0/16")

	if err := m.ReconcilePrivateNetworkFabric(context.Background(), "acct-a", "prod", "fra1", cidr); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	if err := m.ReconcilePrivateNetworkFabric(context.Background(), "acct-a", "prod", "fra1", cidr); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}

	run.mu.Lock()
	defer run.mu.Unlock()
	var linkAdds int
	for _, command := range run.commands {
		if strings.Contains(strings.Join(command, " "), "ip link add") {
			linkAdds++
		}
	}
	if linkAdds != 1 {
		t.Fatalf("bridge link-add calls = %d, want 1; commands=%v", linkAdds, run.commands)
	}
}

func TestRemovePrivateNetworkFabricIsReplaySafeWithCaptureRunner(t *testing.T) {
	run := &fakeRunner{failOn: "ip link del"}
	m := newTestManager(run, &fakeVMM{}).WithCaptureRunner(&fabricMissingCaptureRunner{})
	cidr := netip.MustParsePrefix("10.42.0.0/16")
	if err := m.RemovePrivateNetworkFabric(context.Background(), "acct-a", "prod", "fra1", cidr); err != nil {
		t.Fatalf("replayed remove: %v", err)
	}
}

type fabricMissingCaptureRunner struct{}

func (*fabricMissingCaptureRunner) RunCapture(context.Context, []string) ([]byte, error) {
	return nil, context.Canceled
}
