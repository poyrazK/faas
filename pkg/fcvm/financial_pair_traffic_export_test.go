//go:build linux && metal

package fcvm

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

// Test-only wiring avoids a fcvm -> vmmdgrpc import cycle. The external test
// registers the production RPC server around this fixture's real Manager.
var FinancialPairServeRPC func(context.Context, *Manager, FinancialPairTrafficOptions) (string, func(), error)

type FinancialPairTrafficOptions struct {
	Host                 string
	CA, Certificate, Key []byte // fixture credentials travel over stdin only
}

type FinancialPairTrafficEvent = financialNativeEvent
type FinancialPairTrafficHost = financialNativeHost

type FinancialPairTrafficFixture struct {
	f     *financialNativeFixture
	owner *financialNativeOwner
}

func NewFinancialPairTrafficFixture(t *testing.T, options FinancialPairTrafficOptions) *FinancialPairTrafficFixture {
	t.Helper()
	requireFinancialPair(t)
	f := newFinancialNativeFixture(t)
	f.configs[1].Traffic = &options
	f.start(0, "") // attest the actual controller host as well
	o := f.start(1, "")
	f.activateEgress()
	spec := f.policy.Spec
	spec.LimitMillicents = 1000 // disposable test policy, never a production quota
	var err error
	f.policy, err = f.store.UpdateFinancialBudget(f.ctx, f.app.AccountID, f.policy.ID, f.policy.Revision, "native-traffic-acceptance", spec)
	if err != nil {
		t.Fatal(err)
	}
	return &FinancialPairTrafficFixture{f: f, owner: o}
}

func FinancialPairTrafficHostFacts() FinancialPairTrafficHost           { return financialNativeHostFacts() }
func (p *FinancialPairTrafficFixture) Ready() FinancialPairTrafficEvent { return p.owner.ready }
func (p *FinancialPairTrafficFixture) Instance() string                 { return p.owner.config.Instance }
func (p *FinancialPairTrafficFixture) Node() string                     { return p.owner.config.NodeID }
func (p *FinancialPairTrafficFixture) App() string                      { return p.owner.config.AppID }
func (p *FinancialPairTrafficFixture) Store() *state.PgStore            { return p.f.store }
func (p *FinancialPairTrafficFixture) PublishTarget(t *testing.T) {
	t.Helper()
	node, err := p.f.store.ComputeNodeByID(t.Context(), p.Node())
	if err != nil {
		t.Fatal(err)
	}
	node.TargetURL = p.Ready().RPCTarget
	if _, err := p.f.store.UpsertComputeNodeFromOperator(t.Context(), node); err != nil {
		t.Fatal(err)
	}
}
func (p *FinancialPairTrafficFixture) Lease() financial.ComputeLease { return p.f.lease(p.owner) }
func (p *FinancialPairTrafficFixture) Call(t *testing.T, operation string) FinancialPairTrafficEvent {
	t.Helper()
	return p.owner.must(t, operation)
}
func (p *FinancialPairTrafficFixture) DatabaseAvailable(available bool) {
	p.f.proxies[1].SetAvailable(available)
}
func (p *FinancialPairTrafficFixture) Restart(t *testing.T, freshAttempt bool) {
	t.Helper()
	p.owner.kill(t)
	if freshAttempt {
		p.f.configs[1].Instance, p.f.configs[1].Attempt = uuid.NewString(), uuid.NewString()
		p.f.releaseHold() // explicit manual resume; never revive the old attempt
	}
	p.owner = p.f.start(1, "")
}
func (p *FinancialPairTrafficFixture) Balance(t *testing.T) (financial.ComputeBalance, int64) {
	t.Helper()
	b, err := p.f.store.FinancialComputeBalance(t.Context(), p.f.policy, time.Now())
	if err != nil || b.Egress == nil || b.ReservedMillicents < 0 || b.CommittedMillicents < 0 || b.CommittedMillicents > p.f.policy.Spec.LimitMillicents || b.ReservedMillicents > p.f.policy.Spec.LimitMillicents-b.CommittedMillicents {
		t.Fatalf("cross-host account funding: %+v %v", b, err)
	}
	return b, p.f.policy.Spec.LimitMillicents
}
