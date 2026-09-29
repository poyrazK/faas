package state

import (
	"context"
	"errors"
	"fmt"
)

func (m *MemStore) platformTenantInvocationAllowedLocked(inv Invocation) error {
	if inv.PlatformTenantID == "" {
		return nil
	}
	tenant, ok := m.platformTenants[inv.PlatformTenantID]
	app := m.apps[inv.AppID]
	if !ok || tenant.AccountID != inv.AccountID || app.AccountID != inv.AccountID ||
		(inv.Source != InvocationAsyncInvoke && inv.Source != InvocationReplay) {
		return ErrInvalidArgument
	}
	if tenant.Status != PlatformTenantActive {
		return ErrPlatformTenantSuspended
	}
	return nil
}

// AdmitPlatformTenantInvocation validates the durable identity before a
// synthetic delivery. Legacy unbound cron envelopes need not have a ledger row.
// A claim is the admission boundary: work already dispatching may finish after
// suspension, while the store rejects all subsequent claims until resumption.
func AdmitPlatformTenantInvocation(ctx context.Context, store interface {
	InvocationByID(context.Context, string) (Invocation, error)
	AppByID(context.Context, string) (App, error)
}, appID string, inv Invocation) (Invocation, error) {
	stored, err := store.InvocationByID(ctx, inv.ID)
	if err != nil {
		if inv.PlatformTenantID == "" && errors.Is(err, ErrNotFound) {
			return inv, nil
		}
		return inv, err
	}
	if stored.PlatformTenantID == "" && inv.PlatformTenantID == "" {
		return inv, nil
	}
	if stored.PlatformTenantID == "" || stored.PlatformTenantID != inv.PlatformTenantID ||
		stored.AppID != appID || stored.State != InvocationDispatching {
		return inv, fmt.Errorf("%w: invocation tenant admission", ErrConflict)
	}
	app, err := store.AppByID(ctx, appID)
	if err != nil || app.AccountID != stored.AccountID {
		return inv, ErrNotFound
	}
	// Use the persisted request, including its method, path, body and version pin.
	return stored, nil
}
