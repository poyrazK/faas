package state

import (
	"context"
	"strings"
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
		Surfaces:  make([]ApplyPlatformTenantSurfaceResult, 0, len(in.SurfaceIDs)+len(in.Surfaces)),
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
	if err := m.planPlatformTenantSurfaces(in, &result); err != nil {
		return ApplyPlatformTenantResult{}, err
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
	for i := range result.Surfaces {
		item := &result.Surfaces[i]
		if item.Action == "create" {
			item.Surface.ID = uuid.NewString()
			item.Surface.CreatedAt, item.Surface.UpdatedAt = now, now
			m.tenantSurfaces[item.Surface.ID] = item.Surface
		}
		if item.Action == "link" || item.Action == "create" {
			m.platformTenantBySurface[item.Surface.ID] = result.Tenant.ID
		}
		for j := range item.Hostnames {
			host := &item.Hostnames[j]
			if host.Action != "create" {
				continue
			}
			host.Hostname.ID = uuid.NewString()
			host.Hostname.SurfaceID = item.Surface.ID
			host.Hostname.CreatedAt = now
			m.tenantHostnames[host.Hostname.Hostname] = host.Hostname
		}
	}
	return result, nil
}

func (m *MemStore) planPlatformTenantSurfaces(in ApplyPlatformTenantParams, result *ApplyPlatformTenantResult) error {
	count := 0
	for _, surface := range m.tenantSurfaces {
		if surface.AccountID == in.AccountID && surface.Status != SurfaceStatusDeleted {
			count++
		}
	}
	for _, wanted := range in.Surfaces {
		app, ok := m.apps[wanted.AppID]
		if !ok || app.AccountID != in.AccountID {
			return ErrNotFound
		}
		item := ApplyPlatformTenantSurfaceResult{Action: "create", Hostnames: make([]ApplyPlatformTenantHostnameResult, 0, len(wanted.Hostnames))}
		for _, surface := range m.tenantSurfaces {
			if surface.AccountID == in.AccountID && surface.Status != SurfaceStatusDeleted && strings.EqualFold(surface.Name, wanted.Name) {
				item.Surface = surface
				break
			}
		}
		if item.Surface.ID == "" {
			count++
			if count > in.Limits.TenantSurfacesPerAccount {
				return &TenantSurfaceQuotaError{Limit: in.Limits.TenantSurfacesPerAccount, Observed: count - 1}
			}
			item.Surface = TenantSurface{AccountID: in.AccountID, AppID: wanted.AppID, Name: wanted.Name,
				CertKind: wanted.CertKind, Status: SurfaceStatusPending, CertState: CertStateNone}
		} else {
			if item.Surface.AppID != wanted.AppID || item.Surface.CertKind != wanted.CertKind {
				return ErrConflict
			}
			for _, linked := range result.Surfaces {
				if linked.Surface.ID == item.Surface.ID {
					return ErrInvalidArgument
				}
			}
			owner := m.platformTenantBySurface[item.Surface.ID]
			if owner != "" && owner != result.Tenant.ID {
				return ErrConflict
			}
			item.Action = "link"
			if owner != "" {
				item.Action = "unchanged"
			}
		}
		existingCount := 0
		for name, host := range m.tenantHostnames {
			if name == host.Hostname && host.SurfaceID == item.Surface.ID {
				existingCount++
			}
		}
		for _, wantedHost := range wanted.Hostnames {
			hostResult := ApplyPlatformTenantHostnameResult{Hostname: TenantHostname{
				Hostname: wantedHost.Hostname, ChallengeToken: wantedHost.ChallengeToken}, Action: "create"}
			for _, current := range m.tenantHostnames {
				if !strings.EqualFold(current.Hostname, wantedHost.Hostname) {
					continue
				}
				if item.Surface.ID == "" || current.SurfaceID != item.Surface.ID {
					return ErrConflict
				}
				hostResult.Hostname, hostResult.Action = current, "unchanged"
				break
			}
			if hostResult.Action == "create" {
				existingCount++
				if existingCount > in.Limits.TenantHostnamesPerSurface {
					return &TenantHostnameQuotaError{Limit: in.Limits.TenantHostnamesPerSurface, Observed: existingCount - 1, SurfaceID: item.Surface.ID}
				}
			}
			item.Hostnames = append(item.Hostnames, hostResult)
		}
		result.Surfaces = append(result.Surfaces, item)
	}
	return nil
}
