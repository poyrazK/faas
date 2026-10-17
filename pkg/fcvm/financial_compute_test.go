package fcvm

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/financial"
)

type computeAuthorityFixture struct {
	mu          sync.Mutex
	acquireErr  error
	settleErr   error
	lifetime    time.Duration
	settlements int
	rejected    bool
	lease       financial.ComputeLease
}

func (f *computeAuthorityFixture) CancelFinancialCompute(context.Context, financial.ComputeRequest) (financial.ComputeLease, error) {
	return financial.ComputeLease{}, financial.ErrComputeUnavailable
}

func (f *computeAuthorityFixture) FinancialComputeLease(_ context.Context, account, id string) (financial.ComputeLease, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lease, f.lease.ID == id && f.lease.AccountID == account, nil
}

func (f *computeAuthorityFixture) FinancialComputeRetired(context.Context, string, string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rejected, nil
}

func (f *computeAuthorityFixture) AcquireFinancialCompute(_ context.Context, r financial.ComputeRequest) (financial.ComputeLease, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.acquireErr != nil {
		return financial.ComputeLease{}, f.acquireErr
	}
	now := time.Now()
	life := f.lifetime
	if life == 0 {
		life = time.Hour
	}
	f.lease = financial.ComputeLease{ID: r.LeaseID, Fence: r.Fence, RequestID: r.RequestID, Revision: 1, AttemptID: r.AttemptID, Fenced: r.Fenced, AccountID: r.AccountID, AppID: r.AppID, InstanceID: r.InstanceID, NodeID: r.NodeID, Incarnation: r.Incarnation, Strict: true, BillableMB: r.BillableMB, StartedAt: now, GrantedAt: now, RunUntil: now.Add(life), FundedUntil: now.Add(life + time.Second), Reserved: 100}
	return f.lease, nil
}
func (f *computeAuthorityFixture) RenewFinancialCompute(context.Context, financial.ComputeRequest) (financial.ComputeLease, error) {
	return financial.ComputeLease{}, financial.ErrComputeUnavailable
}
func (f *computeAuthorityFixture) SettleFinancialCompute(_ context.Context, r financial.ComputeRequest) (financial.ComputeLease, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.settlements++
	if f.settleErr != nil {
		return financial.ComputeLease{}, f.settleErr
	}
	now := time.Now()
	f.lease.SettledAt = &now
	f.lease.Reserved = 0
	f.lease.Revision = r.Revision + 1
	f.lease.RequestID = r.RequestID
	return f.lease, nil
}

// adr: 566 — each ordinary app mode reserves before its first physical effect.
func TestFinancialComputeDeniesAllWakeModesBeforeAllocation(t *testing.T) {
	for _, mode := range []string{"cold", "restore", "warm", "command", "execution"} {
		t.Run(mode, func(t *testing.T) {
			v := &fakeVMM{}
			run := &fakeRunner{}
			authority := &computeAuthorityFixture{acquireErr: financial.ErrComputeBudget}
			m := newTestManager(run, v).WithFinancialComputeAuthority(authority, "node", "incarnation")
			req := admissionWake("budget-denied")
			req.AccountID = "account"
			if mode == "restore" || mode == "warm" {
				req.Snapshot = usableSnapshot()
				req.KeepPaused = mode == "warm"
			}
			req.AppTaskOnly = mode == "command"
			req.ExecutionOnly = mode == "execution"
			_, err := m.Wake(t.Context(), req)
			if !errors.Is(err, financial.ErrComputeBudget) || m.alloc.InUse() != 0 || len(run.commands) != 0 || v.bootCount != 0 || len(v.restored) != 0 {
				t.Fatalf("budget bypass: %v", err)
			}
		})
	}
}

func TestFinancialComputeLocalDeadlineSurvivesAuthorityOutage(t *testing.T) {
	v := &fakeVMM{}
	authority := &computeAuthorityFixture{lifetime: 40 * time.Millisecond, settleErr: financial.ErrComputeUnavailable}
	m := newTestManager(&fakeRunner{}, v).WithFinancialComputeAuthority(authority, "node", "incarnation")
	req := admissionWake("local-expiry")
	req.AccountID = "account"
	wakeCtx, cancelWake := context.WithCancel(t.Context())
	defer cancelWake()
	if _, err := m.Wake(wakeCtx, req); err != nil {
		t.Fatal(err)
	}
	cancelWake() // Finishing the initiating RPC cannot cancel monetary retirement.
	deadline := time.Now().Add(2 * time.Second)
	for {
		v.mu.Lock()
		killed := len(v.killed) > 0
		v.mu.Unlock()
		if killed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("local expiry waited for the control plane")
		}
		time.Sleep(time.Millisecond)
	}
	authority.mu.Lock()
	funded := authority.lease.Reserved
	authority.mu.Unlock()
	if funded != 100 || m.alloc.InUse() != 1 {
		t.Fatal("database failure released unconfirmed funding/capacity")
	}
	authority.mu.Lock()
	authority.settleErr = nil
	authority.mu.Unlock()
	if err := m.Destroy(t.Context(), req.Instance); err != nil {
		t.Fatal(err)
	}
	if m.alloc.InUse() != 0 {
		t.Fatal("confirmed settlement did not release capacity")
	}
}

func TestFinancialComputeFailedKillCannotSettle(t *testing.T) {
	v := &fakeVMM{killErr: errors.New("process still present")}
	authority := &computeAuthorityFixture{}
	m := newTestManager(&fakeRunner{}, v).WithFinancialComputeAuthority(authority, "node", "incarnation")
	req := admissionWake("uncertain-teardown")
	req.AccountID = "account"
	if _, err := m.Wake(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if err := m.Destroy(t.Context(), req.Instance); err == nil {
		t.Fatal("unconfirmed kill succeeded")
	}
	authority.mu.Lock()
	calls := authority.settlements
	authority.mu.Unlock()
	if calls != 0 || m.alloc.InUse() != 1 {
		t.Fatal("unconfirmed process returned funded money")
	}
	v.mu.Lock()
	v.killErr = nil
	v.mu.Unlock()
	if err := m.Destroy(t.Context(), req.Instance); err != nil {
		t.Fatal(err)
	}
	authority.mu.Lock()
	calls = authority.settlements
	authority.mu.Unlock()
	if calls != 1 {
		t.Fatalf("settlements=%d", calls)
	}
}

func TestFinancialComputeExpiredAttemptCannotKillReplacement(t *testing.T) {
	v := &fakeVMM{}
	authority := &computeAuthorityFixture{}
	m := newTestManager(&fakeRunner{}, v).WithFinancialComputeAuthority(authority, "node", "incarnation")
	req := admissionWake("reused-financial-id")
	req.AccountID = "account"
	if _, err := m.Wake(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	old := m.computeLeases[req.Instance]
	m.mu.Unlock()
	if err := m.Destroy(t.Context(), req.Instance); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Wake(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	v.mu.Lock()
	kills := len(v.killed)
	v.mu.Unlock()
	if err := m.retireExpiredFinancialCompute(t.Context(), req.Instance, old); err != nil {
		t.Fatal(err)
	}
	v.mu.Lock()
	changed := len(v.killed) != kills
	v.mu.Unlock()
	if changed || m.LiveCount() != 1 {
		t.Fatal("stale monetary timer killed the replacement")
	}
	if err := m.Destroy(t.Context(), req.Instance); err != nil {
		t.Fatal(err)
	}
}
