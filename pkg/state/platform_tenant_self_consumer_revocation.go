package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
)

const MaxPlatformTenantSelfConsumerRevocationBatch = 100

// PlatformTenantSelfConsumerRevocationStore atomically revokes selected
// app-local consumer identities owned by the authenticated platform tenant.
type PlatformTenantSelfConsumerRevocationStore interface {
	RevokePlatformTenantSelfConsumers(context.Context, RevokePlatformTenantSelfConsumersParams) (PlatformTenantSelfConsumerRevocationResult, error)
}

type RevokePlatformTenantSelfConsumersParams struct {
	AccountID   string
	TenantID    string
	ConsumerIDs []string
}

type PlatformTenantSelfConsumerRevocationResult struct {
	Consumers   []APIConsumer
	RevokedKeys int
	Changed     bool
}

var (
	_ PlatformTenantSelfConsumerRevocationStore = (*PgStore)(nil)
	_ PlatformTenantSelfConsumerRevocationStore = (*MemStore)(nil)
)

func normalizePlatformTenantSelfConsumerRevocation(in RevokePlatformTenantSelfConsumersParams) (RevokePlatformTenantSelfConsumersParams, error) {
	if _, err := uuid.Parse(in.AccountID); err != nil {
		return RevokePlatformTenantSelfConsumersParams{}, ErrInvalidArgument
	}
	if _, err := uuid.Parse(in.TenantID); err != nil {
		return RevokePlatformTenantSelfConsumersParams{}, ErrInvalidArgument
	}
	if len(in.ConsumerIDs) == 0 || len(in.ConsumerIDs) > MaxPlatformTenantSelfConsumerRevocationBatch {
		return RevokePlatformTenantSelfConsumersParams{}, ErrInvalidArgument
	}
	out := RevokePlatformTenantSelfConsumersParams{AccountID: in.AccountID, TenantID: in.TenantID,
		ConsumerIDs: make([]string, 0, len(in.ConsumerIDs))}
	seen := make(map[string]struct{}, len(in.ConsumerIDs))
	for _, rawID := range in.ConsumerIDs {
		parsed, err := uuid.Parse(rawID)
		if err != nil {
			return RevokePlatformTenantSelfConsumersParams{}, ErrInvalidArgument
		}
		id := parsed.String()
		if _, duplicate := seen[id]; duplicate {
			return RevokePlatformTenantSelfConsumersParams{}, ErrInvalidArgument
		}
		seen[id] = struct{}{}
		out.ConsumerIDs = append(out.ConsumerIDs, id)
	}
	sort.Strings(out.ConsumerIDs)
	return out, nil
}

func (m *MemStore) RevokePlatformTenantSelfConsumers(_ context.Context, in RevokePlatformTenantSelfConsumersParams) (PlatformTenantSelfConsumerRevocationResult, error) {
	in, err := normalizePlatformTenantSelfConsumerRevocation(in)
	if err != nil {
		return PlatformTenantSelfConsumerRevocationResult{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tenant, ok := m.platformTenants[in.TenantID]
	if !ok || tenant.AccountID != in.AccountID {
		return PlatformTenantSelfConsumerRevocationResult{}, ErrNotFound
	}
	consumers := make([]APIConsumer, len(in.ConsumerIDs))
	for i, id := range in.ConsumerIDs {
		consumer, ok := m.apiConsumers[id]
		if !ok || consumer.AccountID != in.AccountID || consumer.PlatformTenantID != in.TenantID || m.platformTenantByConsumer[id] != in.TenantID {
			return PlatformTenantSelfConsumerRevocationResult{}, ErrNotFound
		}
		consumers[i] = consumer
	}

	now := time.Now().UTC()
	result := PlatformTenantSelfConsumerRevocationResult{Consumers: make([]APIConsumer, len(consumers))}
	consumerIDs := make(map[string]struct{}, len(consumers))
	for i, consumer := range consumers {
		consumerIDs[consumer.ID] = struct{}{}
		if consumer.Status != APIConsumerStatusRevoked || consumer.RevokedAt == nil {
			consumer.Status = APIConsumerStatusRevoked
			consumer.RevokedAt = &now
			consumer.UpdatedAt = now
			m.apiConsumers[consumer.ID] = consumer
			result.Changed = true
		}
		result.Consumers[i] = consumer
	}
	for id, key := range m.consumerKeys {
		if _, selected := consumerIDs[key.ConsumerID]; !selected || key.AccountID != in.AccountID || key.RevokedAt != nil {
			continue
		}
		revokedAt := now
		key.RevokedAt = &revokedAt
		m.consumerKeys[id] = key
		result.RevokedKeys++
		result.Changed = true
	}
	return result, nil
}
