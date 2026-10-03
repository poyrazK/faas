// adr: 471 — an unknown-ID stop reservation cannot authorize recovered reports.
package fcvm

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/netns"
)

func TestFailureReportOwnershipRequiresResourceIdentity(t *testing.T) {
	m := NewManager(nil, nil, Paths{}, "1.7.0", nil, nil)
	stop, err := m.beginInstanceStop(t.Context(), "unknown")
	if err != nil {
		t.Fatal(err)
	}
	defer m.finishInstanceStop("unknown", stop)
	if m.HasInstanceOwnership("unknown") {
		t.Fatal("an unknown-ID stop was mistaken for guest ownership")
	}
	m.retainCleanup(Lease{Instance: "failed-boot"}, netns.Config{}, nil)
	if !m.HasInstanceOwnership("failed-boot") {
		t.Fatal("retained failed-boot resource identity was ignored")
	}
}
