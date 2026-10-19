package fcvm

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/financial"
)

// durableComputeAuthority keeps one durable lease with the ledger's revision
// and request-identity rules, so lost replies can be scripted.
type durableComputeAuthority struct {
	mu          sync.Mutex
	strict      bool
	lease       financial.ComputeLease
	loseRenew   bool // commit the next renewal, then report a timeout
	settleErr   error
	renewals    []financial.ComputeRequest
	settlements int
}

func (a *durableComputeAuthority) CancelFinancialCompute(context.Context, financial.ComputeRequest) (financial.ComputeLease, error) {
	return financial.ComputeLease{}, financial.ErrComputeUnavailable
}

func (a *durableComputeAuthority) FinancialComputeRetired(context.Context, string, string) (bool, error) {
	return false, nil
}

func (a *durableComputeAuthority) FinancialComputeLease(_ context.Context, account, id string) (financial.ComputeLease, bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lease, a.lease.ID == id && a.lease.AccountID == account, nil
}

func (a *durableComputeAuthority) AcquireFinancialCompute(_ context.Context, r financial.ComputeRequest) (financial.ComputeLease, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	a.lease = financial.ComputeLease{ID: r.LeaseID, Fence: r.Fence, RequestID: r.RequestID, RequestOperation: "acquire", Revision: 1, AttemptID: r.AttemptID, Fenced: r.Fenced, AccountID: r.AccountID, AppID: r.AppID, InstanceID: r.InstanceID, NodeID: r.NodeID, Incarnation: r.Incarnation, Strict: a.strict, BillableMB: r.BillableMB, StartedAt: now, GrantedAt: now, RunUntil: now.Add(time.Hour), FundedUntil: now.Add(time.Hour)}
	return a.lease, nil
}

func (a *durableComputeAuthority) mutate(r financial.ComputeRequest, operation string) (financial.ComputeLease, error) {
	if a.lease.RequestID == r.RequestID {
		if a.lease.RequestOperation != operation {
			return financial.ComputeLease{}, financial.ErrComputeFence
		}
		return a.lease, nil
	}
	if a.lease.Revision != r.Revision || a.lease.SettledAt != nil {
		return financial.ComputeLease{}, financial.ErrComputeFence
	}
	now := time.Now()
	a.lease.Revision++
	a.lease.RequestID, a.lease.RequestOperation, a.lease.GrantedAt = r.RequestID, operation, now
	if operation == "settle" {
		a.lease.SettledAt = &now
	} else {
		a.lease.RunUntil = now.Add(time.Hour)
	}
	return a.lease, nil
}

func (a *durableComputeAuthority) RenewFinancialCompute(_ context.Context, r financial.ComputeRequest) (financial.ComputeLease, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.renewals = append(a.renewals, r)
	l, err := a.mutate(r, "renew")
	if err == nil && a.loseRenew {
		a.loseRenew = false
		return financial.ComputeLease{}, context.DeadlineExceeded
	}
	return l, err
}

func (a *durableComputeAuthority) SettleFinancialCompute(_ context.Context, r financial.ComputeRequest) (financial.ComputeLease, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.settlements++
	if a.settleErr != nil {
		return financial.ComputeLease{}, a.settleErr
	}
	return a.mutate(r, "settle")
}

func (a *durableComputeAuthority) durable() financial.ComputeLease {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lease
}

func strictLocalLease(t *testing.T, m *Manager, a *durableComputeAuthority, instance string) *localComputeLease {
	t.Helper()
	l, err := a.AcquireFinancialCompute(t.Context(), financial.ComputeRequest{LeaseID: uuid.NewString(), Fence: uuid.NewString(), RequestID: uuid.NewString(), AccountID: uuid.NewString(), AppID: uuid.NewString(), InstanceID: instance, NodeID: "node", Incarnation: "incarnation", BillableMB: 136})
	if err != nil {
		t.Fatal(err)
	}
	local := &localComputeLease{lease: l, request: computeRequest(l), done: make(chan struct{}), deadline: time.Now().Add(time.Hour)}
	m.mu.Lock()
	m.computeLeases = map[string]*localComputeLease{instance: local}
	m.mu.Unlock()
	return local
}

// adr: 1644 — a renewal whose reply was lost is replayed, not abandoned or
// fenced, and the committed revision is what settlement later uses.
func TestFinancialComputeLostRenewalIsReplayed(t *testing.T) {
	a := &durableComputeAuthority{strict: true, loseRenew: true}
	m := newTestManager(&fakeRunner{}, &fakeVMM{}).WithFinancialComputeAuthority(a, "node", "incarnation")
	local := strictLocalLease(t, m, a, "lost-renewal")
	if renewed, stop := m.renewFinancialComputeCredit(t.Context(), local, false); renewed || stop {
		t.Fatalf("lost reply: renewed=%v stop=%v", renewed, stop)
	}
	if renewed, stop := m.renewFinancialComputeCredit(t.Context(), local, false); !renewed || stop {
		t.Fatalf("replay: renewed=%v stop=%v", renewed, stop)
	}
	if len(a.renewals) != 2 || a.renewals[0].RequestID != a.renewals[1].RequestID {
		t.Fatal("replay minted a new request identity")
	}
	if local.lease.Revision != a.durable().Revision || local.pendingRenew != nil {
		t.Fatalf("local revision %d, durable %d", local.lease.Revision, a.durable().Revision)
	}
	if err := m.settleFinancialCompute(t.Context(), "lost-renewal"); err != nil || a.durable().SettledAt == nil {
		t.Fatalf("settle after replay: %v", err)
	}
}

// adr: 1644 — a denial or fence stops renewal; a transient error does not.
func TestFinancialComputeRenewalStopsOnlyWhenFinal(t *testing.T) {
	a := &durableComputeAuthority{strict: true}
	m := newTestManager(&fakeRunner{}, &fakeVMM{}).WithFinancialComputeAuthority(a, "node", "incarnation")
	local := strictLocalLease(t, m, a, "fenced-renewal")
	a.mu.Lock()
	a.lease.Revision++ // another writer advanced the durable lease
	a.mu.Unlock()
	if renewed, stop := m.renewFinancialComputeCredit(t.Context(), local, false); renewed || !stop {
		t.Fatalf("fenced renewal: renewed=%v stop=%v", renewed, stop)
	}
}

// adr: 1644 — a lost renewal reply cannot leave settlement fenced forever
// after the instance is confirmed absent.
func TestFinancialComputeSettleAdoptsUnseenRevision(t *testing.T) {
	a := &durableComputeAuthority{strict: true}
	m := newTestManager(&fakeRunner{}, &fakeVMM{}).WithFinancialComputeAuthority(a, "node", "incarnation")
	local := strictLocalLease(t, m, a, "stale-settle")
	if _, err := a.RenewFinancialCompute(t.Context(), computeRequest(local.lease)); err != nil {
		t.Fatal(err)
	}
	if err := m.settleFinancialCompute(t.Context(), "stale-settle"); err != nil {
		t.Fatalf("settle with stale revision: %v", err)
	}
	if a.durable().SettledAt == nil || m.computeLeases["stale-settle"] != nil {
		t.Fatal("receipt was not settled and released")
	}
}

// adr: 1644 — an acquire whose reply was lost may adopt only its own receipt.
func TestFinancialComputeRecoveryFencesUncertainAcquire(t *testing.T) {
	a := &durableComputeAuthority{strict: true}
	m := newTestManager(&fakeRunner{}, &fakeVMM{}).WithFinancialComputeAuthority(a, "node", "incarnation")
	local := &localComputeLease{request: financial.ComputeRequest{LeaseID: uuid.NewString()}, done: make(chan struct{})}
	m.computeLeases = map[string]*localComputeLease{"uncertain": local}
	other := financial.ComputeLease{ID: uuid.NewString(), NodeID: "node", InstanceID: "uncertain", Revision: 1, Strict: true}
	if err := m.RecoverFinancialComputeLease(t.Context(), other); !errors.Is(err, financial.ErrComputeFence) {
		t.Fatalf("uncertain acquire adopted another lease: %v", err)
	}
	if local.lease.ID != "" {
		t.Fatal("foreign lease replaced the local attempt")
	}
}

// adr: 1644 — an unbudgeted VM funds nothing, so a failed settlement releases
// its slot and is retried by recovery.
func TestFinancialComputeUnbudgetedSettleFailureReleasesCapacity(t *testing.T) {
	a := &durableComputeAuthority{}
	m := newTestManager(&fakeRunner{}, &fakeVMM{}).WithFinancialComputeAuthority(a, "node", "incarnation")
	req := admissionWake("unbudgeted-settle")
	req.AccountID = "account"
	if _, err := m.Wake(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.settleErr = financial.ErrComputeUnavailable
	a.mu.Unlock()
	if err := m.Destroy(t.Context(), req.Instance); err != nil {
		t.Fatalf("unbudgeted teardown failed on settlement: %v", err)
	}
	if m.alloc.InUse() != 0 {
		t.Fatal("unbudgeted settlement failure retained capacity")
	}
	a.mu.Lock()
	a.settleErr = nil
	a.mu.Unlock()
	if err := m.RecoverFinancialComputeLease(t.Context(), a.durable()); err != nil {
		t.Fatalf("recovery: %v", err)
	}
	if a.durable().SettledAt == nil || m.computeLeases[req.Instance] != nil {
		t.Fatal("recovery did not settle the deferred receipt")
	}
}
