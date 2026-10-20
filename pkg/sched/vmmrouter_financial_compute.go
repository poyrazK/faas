package sched

import (
	"context"

	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

// Reads current policy intent at the dispatch boundary, then compares the
// node's live incarnation with its registered compute authority. A newly
// joined or downgraded node cannot receive strict app work via a legacy RPC.
func (r *VMMRouter) WithFinancialComputeAuthority(store state.FinancialComputeStore) *VMMRouter {
	r.computeAuthority = store
	return r
}

func (r *VMMRouter) checkFinancialComputeOwner(ctx context.Context, node string, app AppSpec) error {
	return r.checkFinancialComputeOwnerForScope(ctx, node, app.AccountID, app.AppID)
}

func (r *VMMRouter) checkFinancialComputeOwnerForScope(ctx context.Context, node, account, key string) error {
	if r.computeAuthority == nil || key == "" {
		return nil
	}
	policies, err := r.computeAuthority.FinancialComputePolicies(ctx, account, key)
	if err != nil {
		return err
	}
	strict := false
	for _, p := range policies {
		strict = strict || p.Spec.Enabled && financial.StrictCompute(p.Spec)
	}
	if !strict {
		return nil
	}
	identity, err := r.RuntimeAdmissionIdentity(ctx, node)
	if err != nil {
		return financial.ErrComputeUnavailable
	}
	valid, err := r.computeAuthority.FinancialComputeOwnerValid(ctx, identity.NodeID, identity.Incarnation)
	if err != nil {
		return err
	}
	if !valid {
		return financial.ErrComputeUnavailable
	}
	return nil
}
