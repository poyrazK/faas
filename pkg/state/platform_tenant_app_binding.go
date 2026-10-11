package state

import "context"

// ValidatePlatformTenantAppBinding confirms that an active platform tenant is
// still linked to an app through a live consumer or active tenant surface.
// Callers use this at admission and again before durable workflow delivery.
func ValidatePlatformTenantAppBinding(ctx context.Context, store PlatformTenantStore, accountID, tenantID, appID string) error {
	tenant, err := store.GetPlatformTenant(ctx, accountID, tenantID)
	if err != nil {
		return err
	}
	if tenant.Status != PlatformTenantActive {
		return ErrPlatformTenantSuspended
	}
	consumers, err := store.ListPlatformTenantConsumers(ctx, accountID, tenantID)
	if err != nil {
		return err
	}
	for _, consumer := range consumers {
		if consumer.AppID == appID && consumer.Active() {
			return nil
		}
	}
	surfaces, err := store.ListPlatformTenantSurfaces(ctx, accountID, tenantID)
	if err != nil {
		return err
	}
	for _, surface := range surfaces {
		if surface.AppID != appID || !surface.Active() {
			continue
		}
		suspended, err := store.PlatformTenantSurfaceSuspended(ctx, surface.ID)
		if err != nil {
			return err
		}
		if !suspended {
			return nil
		}
	}
	return ErrNotFound
}
