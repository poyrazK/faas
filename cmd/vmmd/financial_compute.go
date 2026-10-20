package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

func wireFinancialCompute(ctx context.Context, mgr *fcvm.Manager, store state.Store, native bool) error {
	if store == nil {
		return nil
	}
	compute, ok := store.(state.FinancialComputeStore)
	if !ok {
		return fmt.Errorf("vmmd: financial compute authority unavailable")
	}
	bypass := func(callCtx context.Context, appID string) error {
		app, err := store.AppByID(callCtx, appID)
		if err != nil {
			return errors.Join(financial.ErrComputeUnavailable, err)
		}
		policies, err := compute.FinancialComputePolicies(callCtx, app.AccountID, app.ID)
		if err != nil {
			return errors.Join(financial.ErrComputeUnavailable, err)
		}
		for _, p := range policies {
			if p.Spec.Enabled && financial.StrictCompute(p.Spec) {
				return financial.ErrComputeUnavailable
			}
		}
		return nil
	}
	mgr.WithFinancialComputeBypassGuard(bypass)
	if !native {
		base := managedPostgresAdmissionGuard(store)
		mgr.WithAppAdmissionGuard(func(callCtx context.Context, appID string) error {
			if base != nil {
				if err := base(callCtx, appID); err != nil {
					return err
				}
			}
			return bypass(callCtx, appID)
		})
		return nil
	}
	identity, err := mgr.RuntimeAdmissionIdentity()
	if err != nil {
		return err
	}
	mgr.WithFinancialComputeAuthority(compute, identity.NodeID, identity.Incarnation)
	if err := compute.RegisterFinancialComputeOwner(ctx, identity.NodeID, identity.Incarnation); err != nil {
		return fmt.Errorf("vmmd: register compute authority: %w", err)
	}
	// Recover before opening admission. A failed retirement retains funds and
	// is retried by the independent daemon loop, without publishing an instance.
	recoverLeases := func() {
		callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.FinancialComputeAuthorityTimeout)
		rows, err := compute.FinancialComputeNodeLeases(callCtx, identity.NodeID)
		cancel()
		if err != nil {
			return
		}
		mgr.ForgetUnfundedFinancialComputeAttempts(rows)
		for _, l := range rows {
			retireCtx, retireCancel := context.WithTimeout(context.WithoutCancel(ctx), api.FinancialComputeTeardownReserve)
			_ = mgr.RecoverFinancialComputeLease(retireCtx, l)
			retireCancel()
		}
	}
	recoverLeases()
	go func() {
		tick := time.NewTicker(api.FinancialComputeOwnerHeartbeat)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			callCtx, cancel := context.WithTimeout(ctx, api.FinancialComputeAuthorityTimeout)
			_ = compute.RegisterFinancialComputeOwner(callCtx, identity.NodeID, identity.Incarnation)
			cancel()
			recoverLeases()
		}
	}()
	return nil
}
