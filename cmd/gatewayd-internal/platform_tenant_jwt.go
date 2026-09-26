package main

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type platformTenantExternalRefResolver struct{ store *state.PgStore }

func newPlatformTenantExternalRefResolver(store *state.PgStore) gateway.PlatformTenantExternalRefResolver {
	if store == nil {
		return nil
	}
	return platformTenantExternalRefResolver{store: store}
}

func (a platformTenantExternalRefResolver) ResolvePlatformTenantExternalRef(ctx context.Context, accountID, externalRef string) (gateway.PlatformTenantJWTIdentity, bool, error) {
	tenant, err := a.store.ResolvePlatformTenantExternalRef(ctx, accountID, externalRef)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return gateway.PlatformTenantJWTIdentity{}, false, nil
		}
		return gateway.PlatformTenantJWTIdentity{}, false, err
	}
	return gateway.PlatformTenantJWTIdentity{TenantID: tenant.ID, Active: tenant.Status == state.PlatformTenantActive}, true, nil
}
