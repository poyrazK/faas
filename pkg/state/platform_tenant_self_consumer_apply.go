package state

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const MaxPlatformTenantSelfConsumerApplySurfaces = 100

// PlatformTenantSelfConsumerApplyStore creates or replays one customer
// identity per selected, already-linked app surface as a single transaction.
type PlatformTenantSelfConsumerApplyStore interface {
	ApplyPlatformTenantSelfConsumers(context.Context, ApplyPlatformTenantSelfConsumersParams) (PlatformTenantSelfConsumersResult, error)
}

type ApplyPlatformTenantSelfConsumersParams struct {
	AccountID   string
	TenantID    string
	ExternalRef string
	Name        string
	SurfaceIDs  []string
	DryRun      bool
}

type PlatformTenantSelfConsumerApplyItem struct {
	SurfaceID string
	Consumer  APIConsumer
	Created   bool
}

type PlatformTenantSelfConsumersResult struct {
	Consumers []PlatformTenantSelfConsumerApplyItem
	Changed   bool
}

var (
	_ PlatformTenantSelfConsumerApplyStore = (*PgStore)(nil)
	_ PlatformTenantSelfConsumerApplyStore = (*MemStore)(nil)
)

func normalizePlatformTenantSelfConsumerApply(in ApplyPlatformTenantSelfConsumersParams) (ApplyPlatformTenantSelfConsumersParams, error) {
	if _, err := uuid.Parse(in.AccountID); err != nil {
		return ApplyPlatformTenantSelfConsumersParams{}, ErrInvalidArgument
	}
	if _, err := uuid.Parse(in.TenantID); err != nil {
		return ApplyPlatformTenantSelfConsumersParams{}, ErrInvalidArgument
	}
	if in.ExternalRef == "" || len(in.ExternalRef) > 256 || strings.TrimSpace(in.ExternalRef) != in.ExternalRef ||
		in.Name == "" || len(in.Name) > 128 || strings.TrimSpace(in.Name) != in.Name ||
		len(in.SurfaceIDs) == 0 || len(in.SurfaceIDs) > MaxPlatformTenantSelfConsumerApplySurfaces {
		return ApplyPlatformTenantSelfConsumersParams{}, ErrInvalidArgument
	}
	seen := make(map[string]struct{}, len(in.SurfaceIDs))
	ids := make([]string, 0, len(in.SurfaceIDs))
	for _, rawID := range in.SurfaceIDs {
		parsed, err := uuid.Parse(rawID)
		if err != nil {
			return ApplyPlatformTenantSelfConsumersParams{}, ErrInvalidArgument
		}
		id := parsed.String()
		if _, duplicate := seen[id]; duplicate {
			return ApplyPlatformTenantSelfConsumersParams{}, ErrInvalidArgument
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	in.SurfaceIDs = ids
	return in, nil
}

func (m *MemStore) ApplyPlatformTenantSelfConsumers(_ context.Context, in ApplyPlatformTenantSelfConsumersParams) (PlatformTenantSelfConsumersResult, error) {
	in, err := normalizePlatformTenantSelfConsumerApply(in)
	if err != nil {
		return PlatformTenantSelfConsumersResult{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	tenant, ok := m.platformTenants[in.TenantID]
	if !ok || tenant.AccountID != in.AccountID {
		return PlatformTenantSelfConsumersResult{}, ErrNotFound
	}
	if tenant.Status != PlatformTenantActive {
		return PlatformTenantSelfConsumersResult{}, ErrConflict
	}

	type selectedSurface struct {
		id    string
		appID string
	}
	surfaces := make([]selectedSurface, 0, len(in.SurfaceIDs))
	seenApps := make(map[string]struct{}, len(in.SurfaceIDs))
	for _, surfaceID := range in.SurfaceIDs {
		surface, ok := m.tenantSurfaces[surfaceID]
		if !ok || surface.AccountID != in.AccountID || m.platformTenantBySurface[surfaceID] != in.TenantID || !surface.Active() {
			return PlatformTenantSelfConsumersResult{}, ErrNotFound
		}
		if _, duplicate := seenApps[surface.AppID]; duplicate {
			return PlatformTenantSelfConsumersResult{}, ErrInvalidArgument
		}
		seenApps[surface.AppID] = struct{}{}
		surfaces = append(surfaces, selectedSurface{id: surfaceID, appID: surface.AppID})
	}

	result := PlatformTenantSelfConsumersResult{Consumers: make([]PlatformTenantSelfConsumerApplyItem, 0, len(surfaces))}
	newItems := make([]int, 0, len(surfaces))
	for _, surface := range surfaces {
		item := PlatformTenantSelfConsumerApplyItem{SurfaceID: surface.id}
		found := false
		for _, consumer := range m.apiConsumers {
			if consumer.AccountID != in.AccountID || consumer.AppID != surface.appID || consumer.ExternalRef != in.ExternalRef {
				continue
			}
			if !samePlatformTenantSelfConsumer(consumer, in.TenantID, in.Name) {
				return PlatformTenantSelfConsumersResult{}, ErrConflict
			}
			item.Consumer = consumer
			found = true
			break
		}
		if !found {
			item.Consumer = APIConsumer{AccountID: in.AccountID, AppID: surface.appID,
				PlatformTenantID: in.TenantID, ExternalRef: in.ExternalRef, Name: in.Name,
				Status: APIConsumerStatusActive}
			item.Created = true
			newItems = append(newItems, len(result.Consumers))
		}
		result.Consumers = append(result.Consumers, item)
	}
	if len(newItems) == 0 {
		return result, nil
	}
	policy, ok := m.platformTenantConsumerPolicies[in.TenantID]
	if !ok || !policy.Enabled {
		return PlatformTenantSelfConsumersResult{}, ErrPlatformTenantConsumerProvisioningDisabled
	}
	active := 0
	for _, consumer := range m.apiConsumers {
		if consumer.AccountID == in.AccountID && consumer.PlatformTenantID == in.TenantID && consumer.Active() {
			active++
		}
	}
	if active+len(newItems) > policy.MaxConsumers {
		return PlatformTenantSelfConsumersResult{}, &PlatformTenantConsumerProvisioningQuotaError{
			Limit: policy.MaxConsumers, Observed: active + len(newItems),
		}
	}
	if in.DryRun {
		return result, nil
	}
	for _, index := range newItems {
		consumer := result.Consumers[index].Consumer
		now := time.Now().UTC()
		consumer.ID, consumer.CreatedAt, consumer.UpdatedAt = uuid.NewString(), now, now
		m.apiConsumers[consumer.ID] = consumer
		m.platformTenantByConsumer[consumer.ID] = in.TenantID
		result.Consumers[index].Consumer = consumer
	}
	result.Changed = true
	return result, nil
}
