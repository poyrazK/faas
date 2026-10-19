//go:build linux && metal

package fcvm

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
)

// adr: 566 — real execution must end locally when renewal is unavailable.
// Full persisted-native recovery and cross-daemon cases remain separate gates
// in docs/runbooks/financial-strict-compute-acceptance.md.
func TestMetalFinancialBudgetComputeDeadline(t *testing.T) {
	kernel, base, layer := metalImages(t)
	withCgroupRootAt(t, "/sys/fs/cgroup")
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	authority := &computeAuthorityFixture{lifetime: 30 * time.Second}
	m := newMetalManager(t, kernel).WithFinancialComputeAuthority(authority, "native-node", "native-incarnation")
	req := admissionWake("financial-compute-deadline")
	req.AccountID = "native-account"
	req.BaseKey = base
	req.LayerKey = layer
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 30*time.Second)
		defer done()
		if err := m.Destroy(cleanup, req.Instance); err != nil {
			t.Errorf("financial native cleanup: %v", err)
		}
	})
	if _, err := m.Wake(ctx, req); err != nil {
		t.Fatal(err)
	}
	if m.LiveCount() != 1 {
		t.Fatal("fixture did not start a real guest")
	}
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for m.LiveCount() != 0 || m.LeasedCount() != 0 {
		select {
		case <-ctx.Done():
			t.Fatal("VM outlived its last funded window")
		case <-tick.C:
		}
	}
	authority.mu.Lock()
	settled := authority.lease.SettledAt != nil && authority.lease.Reserved == 0
	authority.mu.Unlock()
	if !settled {
		t.Fatal("native shutdown did not settle its funding")
	}
	leakcheck.AssertZero(t)
}
