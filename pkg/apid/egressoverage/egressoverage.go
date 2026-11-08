// Package egressoverage is the apid side of a billing subject's egress
// overage election (ADR-20261104204500): reading it, turning it on or off,
// and describing what it does on the subject's plan. Without an election
// every plan stops at its monthly egress allowance (ADR-20261104090000).
package egressoverage

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

// ErrNotElectable is returned when the plan cannot elect egress overage.
var ErrNotElectable = errors.New("egressoverage: plan cannot elect egress overage")

// View describes subject's election on plan. provider prices egress; a nil
// provider, or one that does not bill the plan's egress live as of now,
// reports billing off.
func View(provider billing.Provider, plan api.Plan, election state.EgressOverageElection, now time.Time) api.EgressOverageResponse {
	limits, _ := api.LimitsFor(plan)
	out := api.EgressOverageResponse{
		Enabled:     election.On(),
		Electable:   limits.EgressOverageElectable,
		EnabledAt:   election.EnabledAt,
		DisabledAt:  election.DisabledAt,
		IncludedGiB: limits.MonthlyEgressGiB,
		Billing:     api.EgressOverageBillingOff,
	}
	policies, ok := provider.(billing.MeterUsagePolicyProvider)
	if !ok || !limits.EgressOverageElectable {
		return out
	}
	policy, configured := policies.MeterUsagePolicy(plan, state.BillingMeterEgress)
	if !configured || policy.Mode != billing.MeterDeliveryLive || policy.EffectiveFrom.After(now) || policy.UnitQuantity <= 0 {
		return out
	}
	out.Billing = api.EgressOverageBillingLive
	out.PriceMillicentsPerGiB = perGiB(policy)
	out.Currency = financial.BillingCurrency
	return out
}

// perGiB converts the policy's price per unit to a price per GiB, rounded up
// so the shown price never understates the charge.
func perGiB(policy billing.MeterUsagePolicy) int64 {
	n := new(big.Int).Mul(big.NewInt(policy.MillicentsPerUnit), big.NewInt(1<<30))
	q, r := new(big.Int).QuoRem(n, big.NewInt(policy.UnitQuantity), new(big.Int))
	if r.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	if !q.IsInt64() {
		return 0
	}
	return q.Int64()
}

// Set turns subjectID's election on or off at now. Turning it on needs a
// plan that allows it; turning it off never does, so a subject that moved to
// Free can still end an election.
func Set(ctx context.Context, store state.EgressOverageStore, subjectID string, plan api.Plan, on bool, now time.Time) (state.EgressOverageElection, error) {
	if on {
		if limits, ok := api.LimitsFor(plan); !ok || !limits.EgressOverageElectable {
			return state.EgressOverageElection{}, ErrNotElectable
		}
	}
	return store.SetEgressOverage(ctx, subjectID, on, now)
}

// NotElectableProblem is the 402 a plan that cannot elect egress overage
// gets, naming the plan and its allowance.
func NotElectableProblem(plan api.Plan) *api.Problem {
	limits, _ := api.LimitsFor(plan)
	p := api.NewProblem(http.StatusPaymentRequired, api.CodePlanEgressOverageNotAllowed,
		"Plan cannot elect egress overage",
		fmt.Sprintf("the %s plan stops at its %d GiB monthly egress allowance; upgrade to a paid plan to pay for egress above it", plan, limits.MonthlyEgressGiB))
	p.DocsURL = api.EgressOverageDocsURL
	return p
}
