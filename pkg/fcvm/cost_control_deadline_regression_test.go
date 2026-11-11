package fcvm

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/netns"
)

type costControlBlockingNft struct {
	entered chan bool
	release chan struct{}
	once    sync.Once
}

func (r *costControlBlockingNft) Run(ctx context.Context, argv []string) error {
	return r.RunInput(ctx, argv, nil)
}
func (r *costControlBlockingNft) RunInput(ctx context.Context, _ []string, _ []byte) error {
	_, bounded := ctx.Deadline()
	r.once.Do(func() { r.entered <- bounded })
	select {
	case <-r.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type costControlRenewAuthority struct {
	FinancialComputeAuthority
	lease financial.ComputeLease
}

func (a *costControlRenewAuthority) RenewFinancialCompute(_ context.Context, _ financial.ComputeRequest) (financial.ComputeLease, error) {
	out := a.lease
	out.GrantedAt = time.Now()
	out.Revision++
	out.EgressSequence++
	out.EgressGranted += out.EgressGrant
	return out, nil
}

// Near the UTC period boundary, a renewal can retain the same RunUntil.
// A stuck nft operation must not block the separate physical-retirement timer.
func TestCostControlEgressHelperCannotBlockFinancialDeadline(t *testing.T) {
	deadline := time.Now().Add(7 * time.Second)
	lease := financial.ComputeLease{InstanceID: "audit-funded", Strict: true, EgressFunded: true, EgressGrant: api.FinancialEgressCreditBytes, EgressGranted: api.FinancialEgressCreditBytes, EgressSequence: 1, RunUntil: deadline, FundedUntil: deadline.Add(api.FinancialComputeTeardownReserve), GrantedAt: time.Now(), Revision: 1}
	runner := &costControlBlockingNft{entered: make(chan bool, 1), release: make(chan struct{})}
	cancelled := make(chan struct{})
	var cancelOnce sync.Once
	local := &localComputeLease{lease: lease, deadline: deadline, done: make(chan struct{}), egressApplied: 1, cancelBoot: func() { cancelOnce.Do(func() { close(cancelled) }) }}
	m := &Manager{run: runner, computeAuthority: &costControlRenewAuthority{lease: lease}, live: map[string]*Instance{"audit-funded": {Net: netns.Config{Instance: "audit-funded", Netns: "audit-ns", VethHost: "audit-vh", VethPeer: "audit-vp", EgressBudgetBytes: api.FinancialEgressCreditBytes}}}, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	m.financialEgressRead = func(string) (uint64, error) { return uint64(api.FinancialEgressCreditBytes / 4), nil }
	finished := make(chan struct{})
	go func() { defer close(finished); m.watchFinancialCompute(context.Background(), local) }()
	defer func() {
		close(local.done)
		close(runner.release)
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Error("watcher did not join cleanup")
		}
	}()
	select {
	case bounded := <-runner.entered:
		if !bounded {
			t.Log("nft helper received a context with no deadline")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watcher never entered the egress refill")
	}
	timer := time.NewTimer(time.Until(deadline.Add(150 * time.Millisecond)))
	defer timer.Stop()
	<-timer.C
	select {
	case <-cancelled:
	default:
		t.Error("financial deadline passed while nft helper blocked; retirement/cancelBoot was never initiated")
	}
}
