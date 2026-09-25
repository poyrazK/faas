package state

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

func (m *MemStore) CreatePlatformTenant(_ context.Context, accountID, externalRef, name string, limit int) (PlatformTenant, bool, error) {
	if err := validatePlatformTenantInput(accountID, externalRef, name); err != nil {
		return PlatformTenant{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[accountID]; !ok {
		return PlatformTenant{}, false, ErrNotFound
	}
	count := 0
	for _, tenant := range m.platformTenants {
		if tenant.AccountID == accountID && tenant.ExternalRef == externalRef {
			if tenant.Name != name {
				return PlatformTenant{}, false, ErrConflict
			}
			return tenant, false, nil
		}
		if tenant.AccountID == accountID {
			count++
		}
	}
	if count >= limit {
		return PlatformTenant{}, false, &PlatformTenantQuotaError{Limit: limit, Observed: count}
	}
	now := time.Now().UTC()
	tenant := PlatformTenant{ID: uuid.NewString(), AccountID: accountID, ExternalRef: externalRef,
		Name: name, Status: PlatformTenantActive, CreatedAt: now, UpdatedAt: now}
	m.platformTenants[tenant.ID] = tenant
	return tenant, true, nil
}

func (m *MemStore) GetPlatformTenant(_ context.Context, accountID, tenantID string) (PlatformTenant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tenant, ok := m.platformTenants[tenantID]
	if !ok || tenant.AccountID != accountID {
		return PlatformTenant{}, ErrNotFound
	}
	return tenant, nil
}

func (m *MemStore) ListPlatformTenants(_ context.Context, accountID string, limit, offset int) ([]PlatformTenant, error) {
	if limit < 1 || limit > 100 || offset < 0 {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []PlatformTenant{}
	for _, tenant := range m.platformTenants {
		if tenant.AccountID == accountID {
			out = append(out, tenant)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if offset >= len(out) {
		return []PlatformTenant{}, nil
	}
	end := offset + limit
	if end > len(out) {
		end = len(out)
	}
	return out[offset:end], nil
}

func (m *MemStore) SetPlatformTenantStatus(_ context.Context, accountID, tenantID, status string) (PlatformTenant, error) {
	if !validPlatformTenantStatus(status) {
		return PlatformTenant{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tenant, ok := m.platformTenants[tenantID]
	if !ok || tenant.AccountID != accountID {
		return PlatformTenant{}, ErrNotFound
	}
	tenant.Status = status
	tenant.UpdatedAt = time.Now().UTC()
	m.platformTenants[tenant.ID] = tenant
	return tenant, nil
}

func (m *MemStore) LinkPlatformTenantConsumer(_ context.Context, accountID, tenantID, consumerID string) (APIConsumer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tenant, ok := m.platformTenants[tenantID]
	if !ok || tenant.AccountID != accountID {
		return APIConsumer{}, ErrNotFound
	}
	consumer, ok := m.apiConsumers[consumerID]
	if !ok || consumer.AccountID != accountID {
		return APIConsumer{}, ErrNotFound
	}
	if attached := m.platformTenantByConsumer[consumerID]; attached != "" && attached != tenantID {
		return APIConsumer{}, ErrConflict
	}
	m.platformTenantByConsumer[consumerID] = tenantID
	consumer.PlatformTenantID = tenantID
	m.apiConsumers[consumerID] = consumer
	return consumer, nil
}

func (m *MemStore) LinkPlatformTenantSurface(_ context.Context, accountID, tenantID, surfaceID string) (TenantSurface, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tenant, ok := m.platformTenants[tenantID]
	if !ok || tenant.AccountID != accountID {
		return TenantSurface{}, ErrNotFound
	}
	surface, ok := m.tenantSurfaces[surfaceID]
	if !ok || surface.AccountID != accountID || surface.Status == SurfaceStatusDeleted {
		return TenantSurface{}, ErrNotFound
	}
	if attached := m.platformTenantBySurface[surfaceID]; attached != "" && attached != tenantID {
		return TenantSurface{}, ErrConflict
	}
	m.platformTenantBySurface[surfaceID] = tenantID
	return surface, nil
}

func (m *MemStore) ListPlatformTenantConsumers(_ context.Context, accountID, tenantID string) ([]APIConsumer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if tenant, ok := m.platformTenants[tenantID]; !ok || tenant.AccountID != accountID {
		return nil, ErrNotFound
	}
	out := []APIConsumer{}
	for consumerID, attached := range m.platformTenantByConsumer {
		if attached == tenantID {
			if consumer, ok := m.apiConsumers[consumerID]; ok && consumer.AccountID == accountID {
				out = append(out, consumer)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AppID == out[j].AppID {
			return out[i].ID < out[j].ID
		}
		return out[i].AppID < out[j].AppID
	})
	return out, nil
}

func (m *MemStore) ListPlatformTenantSurfaces(_ context.Context, accountID, tenantID string) ([]TenantSurface, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if tenant, ok := m.platformTenants[tenantID]; !ok || tenant.AccountID != accountID {
		return nil, ErrNotFound
	}
	out := []TenantSurface{}
	for surfaceID, attached := range m.platformTenantBySurface {
		if attached == tenantID {
			if surface, ok := m.tenantSurfaces[surfaceID]; ok && surface.AccountID == accountID && surface.Status != SurfaceStatusDeleted {
				out = append(out, surface)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AppID == out[j].AppID {
			return out[i].ID < out[j].ID
		}
		return out[i].AppID < out[j].AppID
	})
	return out, nil
}

func (m *MemStore) ListPlatformTenantUsage(_ context.Context, accountID, tenantID string, since, until time.Time) ([]APIConsumerUsageBucket, error) {
	if !until.After(since) {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if tenant, ok := m.platformTenants[tenantID]; !ok || tenant.AccountID != accountID {
		return nil, ErrNotFound
	}
	byDay := map[string]APIConsumerUsageBucket{}
	for key, bucket := range m.platformTenantUsage {
		if bucket.AccountID != accountID || !strings.HasPrefix(key, accountID+"\x00"+tenantID+"\x00") ||
			bucket.WindowStart.Before(since) || !bucket.WindowStart.Before(until) {
			continue
		}
		bucket.WindowStart = bucket.WindowStart.UTC().Truncate(24 * time.Hour)
		key := bucket.AppID + "/" + bucket.ConsumerKey + "/" + bucket.SurfaceID + "/" + bucket.WindowStart.Format(time.RFC3339)
		day := byDay[key]
		day.AccountID, day.AppID, day.ConsumerKey, day.SurfaceID, day.WindowStart = bucket.AccountID, bucket.AppID, bucket.ConsumerKey, bucket.SurfaceID, bucket.WindowStart
		day.RequestCount += bucket.RequestCount
		day.ErrorCount += bucket.ErrorCount
		day.BillableUnits += bucket.BillableUnits
		byDay[key] = day
	}
	out := make([]APIConsumerUsageBucket, 0, len(byDay))
	for _, day := range byDay {
		out = append(out, day)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].WindowStart.Equal(out[j].WindowStart) {
			if out[i].AppID == out[j].AppID {
				if out[i].ConsumerKey == out[j].ConsumerKey {
					return out[i].SurfaceID < out[j].SurfaceID
				}
				return out[i].ConsumerKey < out[j].ConsumerKey
			}
			return out[i].AppID < out[j].AppID
		}
		return out[i].WindowStart.Before(out[j].WindowStart)
	})
	return out, nil
}

func (m *MemStore) PlatformTenantSurfaceSuspended(_ context.Context, surfaceID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.tenantSurfaces[surfaceID]; !ok {
		return false, ErrNotFound
	}
	tenantID := m.platformTenantBySurface[surfaceID]
	if tenant, ok := m.platformTenants[tenantID]; ok {
		return tenant.Status == PlatformTenantSuspended, nil
	}
	return false, nil
}

func (m *MemStore) PlatformTenantHostBinding(_ context.Context, host string) (PlatformTenantHostBinding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, hostname := range m.tenantHostnames {
		if hostname.Hostname != host {
			continue
		}
		surface, ok := m.tenantSurfaces[hostname.SurfaceID]
		if !ok || surface.Status == SurfaceStatusDeleted {
			return PlatformTenantHostBinding{}, ErrNotFound
		}
		tenantID := m.platformTenantBySurface[surface.ID]
		tenant := m.platformTenants[tenantID]
		return PlatformTenantHostBinding{
			SurfaceID: surface.ID, AppID: surface.AppID, AccountID: surface.AccountID,
			TenantID: tenantID, Active: surface.Active(), Verified: hostname.Verified(),
			Suspended: tenant.Status == PlatformTenantSuspended,
		}, nil
	}
	return PlatformTenantHostBinding{}, ErrNotFound
}
