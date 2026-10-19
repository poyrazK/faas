//go:build linux && metal

package fcvm

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/financial"
)

// Reuse the production-native funding race with node B executing on a
// distinct host. These are component fixtures, separate from API activation
// and meterd/provider reconciliation in the full-system lane.
func TestMetalFinancialPairSharedBudget(t *testing.T) {
	requireFinancialPair(t)
	t.Run("app_job_compute_egress", TestMetalFinancialEgressPostgresContention)
}

func TestMetalFinancialPairDatabasePartitions(t *testing.T) {
	requireFinancialPair(t)
	t.Run("node_a", func(t *testing.T) { financialPairPartition(t, 0) })
	t.Run("node_b", func(t *testing.T) { financialPairPartition(t, 1) })
}

func financialPairPartition(t *testing.T, node int) {
	t.Helper()
	f := newFinancialNativeFixture(t)
	owners := []*financialNativeOwner{f.start(0, ""), f.start(1, "")}
	f.activateEgress()
	owner := owners[node]
	owner.must(t, "wake")
	f.proxies[node].SetAvailable(false) // drops real retained sessions; observer and peer stay connected
	lease := f.lease(owner)             // join the last possible in-flight database grant
	f.assertBalance(lease.Reserved, lease.Committed)
	financialPairAssertCredits(t, f, lease, false)
	refused := owners[1-node].call(t, financialNativeCommand{Operation: "wake"})
	if refused.Error != "budget" || refused.Journal || refused.Physical != 0 || refused.Leased != 0 {
		t.Fatalf("partition reused another host's retained credits: %+v", refused)
	}
	owner.wait(t, lease.FundedUntil.Add(time.Second), func(e financialNativeEvent) bool {
		return e.Physical == 0 && e.ResourcesRemoved && e.Leased > 0
	})
	if current := f.lease(owner); current.SettledAt != nil || current.EgressGranted != lease.EgressGranted || current.Reserved != lease.Reserved {
		t.Fatalf("partition released funding before reconciliation: %+v", current)
	}
	financialPairAssertCredits(t, f, lease, false)
	owner.kill(t)
	f.proxies[node].SetAvailable(true)
	replacement := f.start(node, "")
	if replacement.identity.Incarnation == owner.identity.Incarnation {
		t.Fatal("replacement reused the crashed owner's incarnation")
	}
	replacement.must(t, "recover")
	financialPairAssertCredits(t, f, lease, true)
	if e := replacement.must(t, "status"); e.Physical != 0 || e.Leased != 0 || !e.ResourcesRemoved {
		t.Fatalf("settlement preceded native absence: %+v", e)
	}
	if e := replacement.call(t, financialNativeCommand{Operation: "wake"}); e.Error != "fence" || e.Leased != 0 {
		t.Fatalf("old funded attempt revived: %+v", e)
	}
}

func TestMetalFinancialPairOwnerRestarts(t *testing.T) {
	requireFinancialPair(t)
	t.Run("node_a", func(t *testing.T) { financialPairRestart(t, 0) })
	t.Run("node_b", func(t *testing.T) { financialPairRestart(t, 1) })
}

func financialPairRestart(t *testing.T, node int) {
	t.Helper()
	f := newFinancialNativeFixture(t)
	owners := []*financialNativeOwner{f.start(0, ""), f.start(1, "")}
	f.activateEgress()
	owner := owners[node]
	owner.must(t, "wake")
	owner.must(t, "offline") // join renewal before the crash
	lease := f.lease(owner)
	owner.kill(t)
	financialPairAssertCredits(t, f, lease, false)
	replacement := f.start(node, "")
	if replacement.identity.Incarnation == owner.identity.Incarnation || replacement.ready.Live != 0 {
		t.Fatal("restart reused or published the previous physical generation")
	}
	replacement.must(t, "settlement-off")
	if e := replacement.call(t, financialNativeCommand{Operation: "recover"}); e.Error == "" || !e.ResourcesRemoved || e.Leased == 0 {
		t.Fatalf("uncertain settlement released native ownership: %+v", e)
	}
	financialPairAssertCredits(t, f, lease, false)
	if e := owners[1-node].call(t, financialNativeCommand{Operation: "wake"}); e.Error != "budget" || e.Journal || e.Physical != 0 {
		t.Fatalf("peer reused unconfirmed retired credits: %+v", e)
	}
	replacement.must(t, "settlement-on")
	replacement.must(t, "recover")
	financialPairAssertCredits(t, f, lease, true)
	final := f.lease(replacement)
	replacement.must(t, "recover")
	if replay := f.lease(replacement); replay.Revision != final.Revision || replay.EgressGranted != final.EgressGranted || replay.Committed != final.Committed {
		t.Fatalf("settlement replay changed the retained receipt: %+v", replay)
	}
	if e := replacement.call(t, financialNativeCommand{Operation: "wake"}); e.Error != "fence" {
		t.Fatalf("retired generation executed again: %+v", e)
	}
}

func financialPairAssertCredits(t *testing.T, f *financialNativeFixture, lease financial.ComputeLease, settled bool) {
	t.Helper()
	b, err := f.store.FinancialComputeBalance(f.ctx, f.policy, time.Now())
	if err != nil || b.Egress == nil || b.ReservedMillicents+b.CommittedMillicents > f.policy.Spec.LimitMillicents {
		t.Fatalf("shared money ceiling: %+v %v", b, err)
	}
	if settled {
		if b.UnconfirmedLeases != 0 || b.Egress.ReservedBytes != 0 || b.Egress.CommittedBytes != lease.EgressGranted {
			t.Fatalf("retired credits not committed exactly once: %+v", b)
		}
	} else if b.UnconfirmedLeases != 1 || b.Egress.ReservedBytes < lease.EgressGranted || b.Egress.CommittedBytes != 0 {
		t.Fatalf("uncertain physical owner released credits: %+v", b)
	}
	t.Logf("financial-pair-balance: settled=%t reserved=%d committed=%d limit=%d egress_reserved=%d egress_committed=%d issued=%d", settled, b.ReservedMillicents, b.CommittedMillicents, f.policy.Spec.LimitMillicents, b.Egress.ReservedBytes, b.Egress.CommittedBytes, lease.EgressGranted)
}
