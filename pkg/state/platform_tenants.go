package state

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// PlatformTenant is one end customer of an account that operates multiple apps.
// App consumers and hostname surfaces remain independently owned resources.
type PlatformTenant struct {
	ID          string
	AccountID   string
	ExternalRef string
	Name        string
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

const (
	PlatformTenantActive    = "active"
	PlatformTenantSuspended = "suspended"
)

// PlatformTenantStore is kept separate from Store so existing narrow test
// doubles do not gain platform-customer lifecycle methods.
type PlatformTenantStore interface {
	CreatePlatformTenant(context.Context, string, string, string, int) (PlatformTenant, bool, error)
	GetPlatformTenant(context.Context, string, string) (PlatformTenant, error)
	ListPlatformTenants(context.Context, string, int, int) ([]PlatformTenant, error)
	SetPlatformTenantStatus(context.Context, string, string, string) (PlatformTenant, error)
	LinkPlatformTenantConsumer(context.Context, string, string, string) (APIConsumer, error)
	LinkPlatformTenantSurface(context.Context, string, string, string) (TenantSurface, error)
	ListPlatformTenantConsumers(context.Context, string, string) ([]APIConsumer, error)
	ListPlatformTenantSurfaces(context.Context, string, string) ([]TenantSurface, error)
	ListPlatformTenantUsage(context.Context, string, string, time.Time, time.Time) ([]APIConsumerUsageBucket, error)
	PlatformTenantSurfaceSuspended(context.Context, string) (bool, error)
}

type PlatformTenantQuotaError struct {
	Limit    int
	Observed int
}

func (e *PlatformTenantQuotaError) Error() string {
	return fmt.Sprintf("platform tenant quota reached: %d/%d", e.Observed, e.Limit)
}

var (
	_ PlatformTenantStore = (*PgStore)(nil)
	_ PlatformTenantStore = (*MemStore)(nil)
)

func validatePlatformTenantInput(accountID, externalRef, name string) error {
	externalRef = strings.TrimSpace(externalRef)
	name = strings.TrimSpace(name)
	if accountID == "" || len(externalRef) < 1 || len(externalRef) > 256 || len(name) < 1 || len(name) > 128 {
		return fmt.Errorf("platform tenant: account_id and 1-256 character external_ref and 1-128 character name required")
	}
	return nil
}

func validPlatformTenantStatus(status string) bool {
	return status == PlatformTenantActive || status == PlatformTenantSuspended
}
