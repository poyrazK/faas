package state

import (
	"context"
	"time"

	"github.com/google/uuid"
)

func (m *MemStore) ApplyPlatformTenant(_ context.Context, in ApplyPlatformTenantParams) (ApplyPlatformTenantResult, error) {
	if err := validatePlatformTenantApply(in); err != nil {
		return ApplyPlatformTenantResult{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[in.AccountID]; !ok {
		return ApplyPlatformTenantResult{}, ErrNotFound
	}
	result := ApplyPlatformTenantResult{
		Consumers: make([]ApplyPlatformTenantConsumerResult, 0, len(in.Consumers)),
		Surfaces:  make([]ApplyPlatformTenantSurfaceResult, 0, len(in.SurfaceIDs)),
	}
	count := 0
	for _, tenant := range m.platformTenants {
		if tenant.AccountID != in.AccountID {
			continue
		}
		count++
		if tenant.ExternalRef == in.ExternalRef {
			result.Tenant = tenant
		}
	}
	if result.Tenant.ID == "" {
		if count >= in.TenantLimit {
			return ApplyPlatformTenantResult{}, &PlatformTenantQuotaError{Limit: in.TenantLimit, Observed: count}
		}
		result.Action = "create"
		result.Tenant = PlatformTenant{AccountID: in.AccountID, ExternalRef: in.ExternalRef,
			Name: in.Name, Status: PlatformTenantActive}
	} else if result.Tenant.Name != in.Name {
		return ApplyPlatformTenantResult{}, ErrConflict
	} else {
		result.Action = "unchanged"
	}
	for _, wanted := range in.Consumers {
		app, ok := m.apps[wanted.AppID]
		if !ok || app.AccountID != in.AccountID {
			return ApplyPlatformTenantResult{}, ErrNotFound
		}
		var current APIConsumer
		for _, consumer := range m.apiConsumers {
			if consumer.AppID == wanted.AppID && consumer.ExternalRef == wanted.ExternalRef {
				current = consumer
				break
			}
		}
		item := ApplyPlatformTenantConsumerResult{Consumer: current, Action: "create"}
		if current.ID == "" {
			item.Consumer = APIConsumer{AccountID: in.AccountID, AppID: wanted.AppID,
				ExternalRef: wanted.ExternalRef, Name: wanted.Name, Status: APIConsumerStatusActive}
		} else {
			if current.AccountID != in.AccountID || current.Name != wanted.Name || !current.Active() {
				return ApplyPlatformTenantResult{}, ErrConflict
			}
			linked := m.platformTenantByConsumer[current.ID]
			if linked != "" && linked != result.Tenant.ID {
				return ApplyPlatformTenantResult{}, ErrConflict
			}
			item.Action = "link"
			if linked != "" && linked == result.Tenant.ID {
				item.Action = "unchanged"
			}
		}
		result.Consumers = append(result.Consumers, item)
	}
	for _, id := range in.SurfaceIDs {
		surface, ok := m.tenantSurfaces[id]
		if !ok || surface.AccountID != in.AccountID || surface.Status == SurfaceStatusDeleted {
			return ApplyPlatformTenantResult{}, ErrNotFound
		}
		linked := m.platformTenantBySurface[id]
		if linked != "" && linked != result.Tenant.ID {
			return ApplyPlatformTenantResult{}, ErrConflict
		}
		item := ApplyPlatformTenantSurfaceResult{Surface: surface, Action: "link"}
		if linked != "" && linked == result.Tenant.ID {
			item.Action = "unchanged"
		}
		result.Surfaces = append(result.Surfaces, item)
	}
	if in.DryRun {
		return result, nil
	}
	now := time.Now().UTC()
	if result.Action == "create" {
		result.Tenant.ID = uuid.NewString()
		result.Tenant.CreatedAt, result.Tenant.UpdatedAt = now, now
		m.platformTenants[result.Tenant.ID] = result.Tenant
	}
	for i := range result.Consumers {
		item := &result.Consumers[i]
		if item.Action == "create" {
			item.Consumer.ID = uuid.NewString()
			item.Consumer.CreatedAt, item.Consumer.UpdatedAt = now, now
		}
		if item.Action != "unchanged" {
			item.Consumer.PlatformTenantID = result.Tenant.ID
			m.apiConsumers[item.Consumer.ID] = item.Consumer
			m.platformTenantByConsumer[item.Consumer.ID] = result.Tenant.ID
		}
	}
	for _, item := range result.Surfaces {
		if item.Action == "link" {
			m.platformTenantBySurface[item.Surface.ID] = result.Tenant.ID
		}
	}
	return result, nil
}
