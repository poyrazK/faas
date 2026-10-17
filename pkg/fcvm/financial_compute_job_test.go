package fcvm

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/financial"
)

// adr: 1641 — ordinary, held and interactive jobs all fund before allocation.
func TestFinancialComputeJobDeniesBeforeAllocation(t *testing.T) {
	for _, mode := range []string{"ordinary", "held", "interactive"} {
		t.Run(mode, func(t *testing.T) {
			runner, v := &fakeRunner{}, &fakeVMM{}
			m := newTestManager(runner, v).WithFinancialComputeAuthority(&computeAuthorityFixture{acquireErr: financial.ErrComputeBudget}, "node", "incarnation")
			r := testJobBootRequest(uuid.NewString())
			r.RunID = uuid.NewString()
			r.LeaseToken = uuid.NewString()
			if mode == "held" {
				r.StartHeld = true
			}
			if mode == "interactive" {
				r.Interactive = &JobInteractive{AttachTimeoutSec: 30}
			}
			if _, err := m.BootJob(t.Context(), r); !errors.Is(err, financial.ErrComputeBudget) {
				t.Fatalf("job funding: %v", err)
			}
			if m.LeasedCount() != 0 || m.LiveCount() != 0 || len(runner.commands) != 0 || len(v.jobBootSpecs) != 0 {
				t.Fatal("unfunded job performed physical work")
			}
		})
	}
}

// A job cannot depend on its initiating RPC, scheduler or database to end its
// execution window. Neither failed kill nor failed settlement returns funding.
func TestFinancialComputeJobDeadlineRetainsUnconfirmedFunding(t *testing.T) {
	for _, failedKill := range []bool{false, true} {
		name := "settlement_outage"
		if failedKill {
			name = "failed_kill"
		}
		t.Run(name, func(t *testing.T) {
			v := &fakeVMM{}
			a := &computeAuthorityFixture{lifetime: 40 * time.Millisecond}
			if failedKill {
				v.killErr = errors.New("native process still present")
			} else {
				a.settleErr = financial.ErrComputeUnavailable
			}
			m := newTestManager(&fakeRunner{}, v).WithFinancialComputeAuthority(a, "node", "incarnation")
			r := testJobBootRequest(uuid.NewString())
			r.RunID = uuid.NewString()
			r.LeaseToken = uuid.NewString()
			ctx, cancel := context.WithCancel(t.Context())
			if _, err := m.BootJob(ctx, r); err != nil {
				cancel()
				t.Fatal(err)
			}
			cancel()
			deadline := time.Now().Add(2 * time.Second)
			for {
				v.mu.Lock()
				killed := len(v.killed) > 0
				v.mu.Unlock()
				if killed {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("job outlived local funding deadline")
				}
				time.Sleep(time.Millisecond)
			}
			a.mu.Lock()
			reserved, calls := a.lease.Reserved, a.settlements
			a.mu.Unlock()
			if reserved != 100 || m.LeasedCount() != 1 || failedKill && calls != 0 {
				t.Fatalf("uncertain cleanup released authority: reserved=%d calls=%d", reserved, calls)
			}
			v.mu.Lock()
			v.killErr = nil
			v.mu.Unlock()
			a.mu.Lock()
			a.settleErr = nil
			a.mu.Unlock()
			for {
				err := m.Destroy(t.Context(), r.Instance)
				if err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal(err)
				}
				time.Sleep(time.Millisecond)
			}
			if m.LeasedCount() != 0 || m.LiveCount() != 0 {
				t.Fatal("confirmed job cleanup did not release capacity")
			}
		})
	}
}
