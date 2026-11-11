package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// PlatformTenantHostBindingReader revalidates a custom hostname's tenant
// routing and suspension without waiting for cache eviction or notification.
type PlatformTenantHostBindingReader interface {
	PlatformTenantHostBinding(context.Context, string) (PlatformTenantHostBinding, error)
}

func (s *pgCore) PlatformTenantHostBinding(ctx context.Context, host string) (PlatformTenantHostBinding, error) {
	data, err := sqlc.New().ReadPublicHostTenantBinding(ctx, s.pool, host)
	return decodePublicHostJSON[PlatformTenantHostBinding](data, err)
}

var (
	_ PlatformTenantHostBindingReader = (*EdgeStore)(nil)
	_ PlatformTenantHostBindingReader = (*MemStore)(nil)
)
