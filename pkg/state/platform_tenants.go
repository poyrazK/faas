package state

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
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

// PlatformTenantHostBinding is the current, uncached ownership of one custom
// hostname. A surface can be unlinked; an empty TenantID is not an identity.
type PlatformTenantHostBinding struct {
	SurfaceID string
	AppID     string
	AccountID string
	TenantID  string
	Active    bool
	Verified  bool
	Suspended bool
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

// PlatformTenantApplyStore reconciles an additive onboarding bundle in one
// transaction. A dry run performs the same ownership and conflict checks but
// leaves no rows behind. Omitted resources are never detached or revoked.
type PlatformTenantApplyStore interface {
	ApplyPlatformTenant(context.Context, ApplyPlatformTenantParams) (ApplyPlatformTenantResult, error)
}

type ApplyPlatformTenantParams struct {
	AccountID   string
	ExternalRef string
	Name        string
	TenantLimit int
	DryRun      bool
	Consumers   []ApplyPlatformTenantConsumer
	SurfaceIDs  []string
}

type ApplyPlatformTenantConsumer struct {
	AppID       string
	ExternalRef string
	Name        string
}

type ApplyPlatformTenantConsumerResult struct {
	Consumer APIConsumer
	Action   string // create, link, unchanged
}

type ApplyPlatformTenantSurfaceResult struct {
	Surface TenantSurface
	Action  string // link, unchanged
}

type ApplyPlatformTenantResult struct {
	Tenant    PlatformTenant
	Action    string // create, unchanged
	Consumers []ApplyPlatformTenantConsumerResult
	Surfaces  []ApplyPlatformTenantSurfaceResult
}

type PlatformTenantQuotaError struct {
	Limit    int
	Observed int
}

func (e *PlatformTenantQuotaError) Error() string {
	return fmt.Sprintf("platform tenant quota reached: %d/%d", e.Observed, e.Limit)
}

var (
	_ PlatformTenantStore      = (*PgStore)(nil)
	_ PlatformTenantStore      = (*MemStore)(nil)
	_ PlatformTenantApplyStore = (*PgStore)(nil)
	_ PlatformTenantApplyStore = (*MemStore)(nil)
)

func validatePlatformTenantApply(in ApplyPlatformTenantParams) error {
	if err := validatePlatformTenantInput(in.AccountID, in.ExternalRef, in.Name); err != nil {
		return err
	}
	if in.TenantLimit < 1 {
		return ErrInvalidArgument
	}
	seenConsumers := make(map[string]bool, len(in.Consumers))
	for _, consumer := range in.Consumers {
		appID, err := uuid.Parse(consumer.AppID)
		if err != nil {
			return ErrInvalidArgument
		}
		if err := validateAPIConsumerPgInput("ApplyPlatformTenant", in.AccountID, consumer.AppID, consumer.ExternalRef, consumer.Name); err != nil {
			return ErrInvalidArgument
		}
		key := appID.String() + "\x00" + consumer.ExternalRef
		if seenConsumers[key] {
			return ErrInvalidArgument
		}
		seenConsumers[key] = true
	}
	seenSurfaces := make(map[string]bool, len(in.SurfaceIDs))
	for _, id := range in.SurfaceIDs {
		surfaceID, err := uuid.Parse(id)
		if err != nil || seenSurfaces[surfaceID.String()] {
			return ErrInvalidArgument
		}
		seenSurfaces[surfaceID.String()] = true
	}
	return nil
}

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
