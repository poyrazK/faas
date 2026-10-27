// adr: 1748 — only a newly paid sequence resets the kernel byte quotas.
package fcvm

import (
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/netns"
)

func egressLocalFixture(t *testing.T) (*Manager, *networkBatchRunner, *localComputeLease) {
	t.Helper()
	run := &networkBatchRunner{}
	m := newTestManager(run, &fakeVMM{})
	nc := netns.NewConfig("funded", "fc-funded", "vh7", "vp7", netip.MustParseAddr("10.100.0.9"))
	nc.EgressBudgetBytes = api.FinancialEgressCreditBytes
	local := &localComputeLease{lease: financial.ComputeLease{InstanceID: "funded", EgressFunded: true, EgressGranted: api.FinancialEgressCreditBytes, EgressGrant: api.FinancialEgressCreditBytes, EgressSequence: 1, RunUntil: time.Now().Add(time.Minute)}, egressApplied: 1, done: make(chan struct{})}
	m.live["funded"] = &Instance{Lease: Lease{Instance: "funded", VethHost: "vh7"}, Net: nc}
	m.financialEgressRead = func(string) (uint64, error) { return 100, nil }
	return m, run, local
}
func TestFinancialEgressUnknownKernelOutcomeCannotReplayCredit(t *testing.T) {
	m, run, local := egressLocalFixture(t)
	run.err = errors.New("reply lost after apply")
	l := local.lease
	l.EgressSequence = 2
	l.EgressGranted *= 2
	if m.syncFinancialEgress(t.Context(), local, l, true) || !local.egressBroken || local.egressApplied != 2 {
		t.Fatal("unknown installation released local sequence")
	}
	calls := len(run.calls)
	run.err = nil
	if m.syncFinancialEgress(t.Context(), local, l, true) || len(run.calls) != calls {
		t.Fatal("uncertain credit was replayed into a fresh quota")
	}
}
func TestFinancialEgressOnlyPaidRefillResetsQuota(t *testing.T) {
	m, run, local := egressLocalFixture(t)
	if !m.syncFinancialEgress(t.Context(), local, local.lease, false) {
		t.Fatal("time-only renewal failed")
	}
	if len(run.calls) != 1 || strings.Contains(run.calls[0].input, "quota") {
		t.Fatal("time renewal minted byte allowance")
	}
	l := local.lease
	l.EgressSequence = 2
	l.EgressGranted *= 2
	if !m.syncFinancialEgress(t.Context(), local, l, true) || local.egressApplied != 2 || local.egressObserved != 100 {
		t.Fatal("newly financed credit was not applied")
	}
	if !strings.Contains(run.calls[1].input, "egress quota 2097152 bytes") || !strings.Contains(run.calls[1].input, "gate quota 3145728 bytes") {
		t.Fatal("kernel limit differs from financed credit")
	}
	calls := len(run.calls)
	if m.syncFinancialEgress(t.Context(), local, l, true) || len(run.calls) != calls {
		t.Fatal("duplicate sequence reset quota")
	}
}
func TestFinancialEgressCounterRegressionStopsRefill(t *testing.T) {
	m, _, local := egressLocalFixture(t)
	local.egressObserved = 101
	refill, err := m.financialEgressLow(local)
	if refill || !errors.Is(err, financial.ErrComputeUnavailable) || !local.egressBroken {
		t.Fatalf("counter reset minted credit: %v %v", refill, err)
	}
}
func TestFinancialEgressClockMustFitFundedWindow(t *testing.T) {
	now := time.Now()
	if financialEgressClockFits(financial.ComputeLease{GrantedAt: now.Add(-time.Minute)}, now) || financialEgressClockFits(financial.ComputeLease{GrantedAt: now.Add(time.Minute)}, now) {
		t.Fatal("unbounded owner clock accepted")
	}
	if !financialEgressClockFits(financial.ComputeLease{GrantedAt: now}, now) {
		t.Fatal("synchronized authority rejected")
	}
}

func TestFinancialEgressNetworklessExecutionKeepsTimeRenewal(t *testing.T) {
	m, _, local := egressLocalFixture(t)
	m.live["funded"].Net = netns.Config{Instance: "funded"}
	m.financialEgressRead = func(string) (uint64, error) {
		t.Fatal("networkless execution attempted to read a nonexistent veth")
		return 0, nil
	}
	refill, err := m.financialEgressLow(local)
	if err != nil || refill || local.egressBroken {
		t.Fatalf("networkless execution lost time funding: %v %v", refill, err)
	}
	if !m.syncFinancialEgress(t.Context(), local, local.lease, false) {
		t.Fatal("networkless time renewal was blocked")
	}
}
