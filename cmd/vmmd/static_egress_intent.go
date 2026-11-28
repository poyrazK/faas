package main

import (
	"context"
	"errors"
	"net/netip"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

// staticEgressIntentStore is what the durable static egress intent read needs.
type staticEgressIntentStore interface {
	AppByID(ctx context.Context, id string) (state.App, error)
	AccountByID(ctx context.Context, id string) (state.Account, error)
	ProvisionedStaticEgressIPExists(ctx context.Context, accountID string, ip netip.Addr) (bool, error)
}

// staticEgressIntent is the app's effective pin (ADR-119,
// ADR-20261128090000): nil for a deleted app or an account whose plan no
// longer allows static egress, matching schedd's wake spec.
// ErrStaticEgressUnprovisioned keeps the pin intent while revoking local
// enforcement when no operator grant covers the address any more.
func staticEgressIntent(ctx context.Context, store staticEgressIntentStore, accountID, appID string) (*netip.Addr, error) {
	app, err := store.AppByID(ctx, appID)
	if errors.Is(err, state.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if app.AccountID != accountID {
		return nil, errors.New("static egress app account mismatch")
	}
	if app.StaticEgressIP == nil {
		return nil, nil
	}
	acct, err := store.AccountByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	pin := state.EffectiveStaticEgressIP(app, acct.Plan)
	if pin == nil {
		return nil, nil
	}
	provisioned, err := store.ProvisionedStaticEgressIPExists(ctx, accountID, *pin)
	if err != nil {
		return nil, err
	}
	if !provisioned {
		return pin, fcvm.ErrStaticEgressUnprovisioned
	}
	return pin, nil
}
