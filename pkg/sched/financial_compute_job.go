// adr: 1641 — a denied boot must not consume a customer execution retry.
package sched

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"github.com/onebox-faas/faas/pkg/state"
)

func financialJobBudgetDenied(err error) bool {
	if errors.Is(err, financial.ErrComputeBudget) {
		return true
	}
	var p *api.Problem
	if errors.As(err, &p) {
		return p.Code == "financial_budget_held"
	}
	p, ok := grpcerr.FromStatus(err)
	return ok && p.Code == "financial_budget_held"
}

// A verified pre-boot financial refusal is infrastructure availability, not
// customer execution. Both the local and gRPC forms must preserve the task.
func financialJobAuthorityUnavailable(err error) bool {
	if errors.Is(err, financial.ErrComputeUnavailable) || errors.Is(err, financial.ErrComputeFence) {
		return true
	}
	var p *api.Problem
	if errors.As(err, &p) {
		return p.Code == "financial_compute_unavailable"
	}
	p, ok := grpcerr.FromStatus(err)
	return ok && p.Code == "financial_compute_unavailable"
}

func (e *Engine) deferFinancialJobClaim(ctx context.Context, run string, index int, instance, node string, token LeaseToken) error {
	compute, ok := e.store.(state.FinancialComputeStore)
	if !ok {
		return financial.ErrComputeUnavailable
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), DestroyTimeout)
	defer cancel()
	if e.vmm != nil {
		if err := e.vmm.Destroy(cleanup, e.nodeForRoute(node), instance); err != nil {
			return err
		}
	}
	if err := compute.DeferFinancialJobClaim(cleanup, run, index, instance, string(token), time.Now().Add(api.SpendCapJobDeferInterval)); err != nil {
		return err
	}
	e.settleJobInstance(cleanup, instance, "financial_budget_held")
	if e.jobLeaser != nil {
		if err := e.jobLeaser.Release(cleanup, token, e.ownerNodeID); err != nil && !errors.Is(err, ErrLeaseNotFound) {
			return err
		}
	}
	_, err := e.store.JobRunRecompute(cleanup, run)
	return err
}
