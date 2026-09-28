package state

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// PlatformTenantSelfConsumerProvisioningStore atomically creates a customer
// identity on an already-linked surface for the authenticated tenant.
type PlatformTenantSelfConsumerProvisioningStore interface {
	CreatePlatformTenantSelfConsumer(context.Context, CreatePlatformTenantSelfConsumerParams) (APIConsumer, bool, error)
}

type CreatePlatformTenantSelfConsumerParams struct {
	AccountID   string
	TenantID    string
	SurfaceID   string
	ExternalRef string
	Name        string
}

var ErrPlatformTenantConsumerProvisioningDisabled = errors.New("state: platform tenant consumer provisioning is disabled")

type PlatformTenantConsumerProvisioningQuotaError struct {
	Limit    int
	Observed int
}

func (e *PlatformTenantConsumerProvisioningQuotaError) Error() string {
	return "platform tenant consumer provisioning limit exceeded"
}

var (
	_ PlatformTenantSelfConsumerProvisioningStore = (*PgStore)(nil)
	_ PlatformTenantSelfConsumerProvisioningStore = (*MemStore)(nil)
)

func validatePlatformTenantSelfConsumerInput(in CreatePlatformTenantSelfConsumerParams) error {
	if _, err := uuid.Parse(in.AccountID); err != nil {
		return ErrInvalidArgument
	}
	if _, err := uuid.Parse(in.TenantID); err != nil {
		return ErrInvalidArgument
	}
	if _, err := uuid.Parse(in.SurfaceID); err != nil {
		return ErrInvalidArgument
	}
	if in.ExternalRef == "" || len(in.ExternalRef) > 256 || strings.TrimSpace(in.ExternalRef) != in.ExternalRef {
		return ErrInvalidArgument
	}
	if in.Name == "" || len(in.Name) > 128 || strings.TrimSpace(in.Name) != in.Name {
		return ErrInvalidArgument
	}
	return nil
}

func samePlatformTenantSelfConsumer(consumer APIConsumer, tenantID, name string) bool {
	return consumer.PlatformTenantID == tenantID && consumer.Name == name && consumer.Active()
}

func (m *MemStore) CreatePlatformTenantSelfConsumer(_ context.Context, in CreatePlatformTenantSelfConsumerParams) (APIConsumer, bool, error) {
	if err := validatePlatformTenantSelfConsumerInput(in); err != nil {
		return APIConsumer{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	tenant, ok := m.platformTenants[in.TenantID]
	if !ok || tenant.AccountID != in.AccountID {
		return APIConsumer{}, false, ErrNotFound
	}
	if tenant.Status != PlatformTenantActive {
		return APIConsumer{}, false, ErrConflict
	}
	surface, ok := m.tenantSurfaces[in.SurfaceID]
	if !ok || surface.AccountID != in.AccountID || m.platformTenantBySurface[in.SurfaceID] != in.TenantID {
		return APIConsumer{}, false, ErrNotFound
	}
	if !surface.Active() {
		return APIConsumer{}, false, ErrNotFound
	}

	// Replays return the original identity even if the owner has since disabled
	// new provisioning or lowered the cap. They never create or relink a row.
	for _, consumer := range m.apiConsumers {
		if consumer.AppID != surface.AppID || consumer.ExternalRef != in.ExternalRef {
			continue
		}
		if !samePlatformTenantSelfConsumer(consumer, in.TenantID, in.Name) {
			return APIConsumer{}, false, ErrConflict
		}
		return consumer, false, nil
	}

	policy, ok := m.platformTenantConsumerPolicies[in.TenantID]
	if !ok || !policy.Enabled {
		return APIConsumer{}, false, ErrPlatformTenantConsumerProvisioningDisabled
	}
	active := 0
	for _, consumer := range m.apiConsumers {
		if consumer.AccountID == in.AccountID && consumer.PlatformTenantID == in.TenantID && consumer.Active() {
			active++
		}
	}
	if active >= policy.MaxConsumers {
		return APIConsumer{}, false, &PlatformTenantConsumerProvisioningQuotaError{Limit: policy.MaxConsumers, Observed: active}
	}

	now := time.Now().UTC()
	consumer := APIConsumer{ID: uuid.NewString(), AccountID: in.AccountID, AppID: surface.AppID,
		PlatformTenantID: in.TenantID, ExternalRef: in.ExternalRef, Name: in.Name,
		Status: APIConsumerStatusActive, CreatedAt: now, UpdatedAt: now}
	m.apiConsumers[consumer.ID] = consumer
	m.platformTenantByConsumer[consumer.ID] = in.TenantID
	return consumer, true, nil
}
