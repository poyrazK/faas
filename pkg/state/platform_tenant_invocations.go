package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

func (m *MemStore) platformTenantInvocationAllowedLocked(inv Invocation) error {
	if inv.PlatformTenantID == "" {
		return nil
	}
	tenant, ok := m.platformTenants[inv.PlatformTenantID]
	app := m.apps[inv.AppID]
	if !ok || tenant.AccountID != inv.AccountID || app.AccountID != inv.AccountID ||
		(inv.Source != InvocationAsyncInvoke && inv.Source != InvocationReplay && inv.Source != InvocationQueue) {
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
	// Cron and trigger batches can use synthetic IDs that cannot address a
	// durable UUID row. They remain unbound; a tenant identity requires a row.
	if _, err := uuid.Parse(inv.ID); err != nil {
		if inv.PlatformTenantID != "" {
			return inv, fmt.Errorf("%w: tenant invocation ID must be a UUID", ErrConflict)
		}
		return inv, nil
	}
	stored, err := store.InvocationByID(ctx, inv.ID)
	if err != nil {
		if inv.PlatformTenantID == "" && inv.Source != InvocationSource("esm") && errors.Is(err, ErrNotFound) {
			return inv, nil
		}
		return inv, err
	}
	if stored.PlatformTenantID == "" && inv.PlatformTenantID == "" {
		if stored.AppID != appID || inv.DeploymentScope != "" && inv.DeploymentScope != stored.DeploymentScope {
			return inv, fmt.Errorf("%w: invocation deployment scope admission", ErrConflict)
		}
		if inv.Source == InvocationSource("esm") && ((stored.Source != InvocationQueue && stored.Source != InvocationDelayedTask) ||
			stored.State != InvocationDispatching || inv.Attempts <= 0 || inv.Attempts != stored.Attempts ||
			inv.ReplayGeneration != stored.ReplayGeneration ||
			stored.LeaseExpiresAt == nil || !stored.LeaseExpiresAt.After(time.Now())) {
			return inv, fmt.Errorf("%w: durable queue invocation claim admission", ErrConflict)
		}
		// DeploymentScope is internal and is omitted from the HTTP envelope.
		// Recover it from durable admission before resolving a wake or target.
		inv.DeploymentScope = stored.DeploymentScope
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
