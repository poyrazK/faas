package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) CreatePlatformTenantWebhookIfUnderQuota(_ context.Context, in AppWebhook, limits api.Limits) (AppWebhook, error) {
	if in.AccountID == "" || in.PlatformTenantID == "" || in.AppID != "" ||
		(in.Scope != "" && in.Scope != AppWebhookScopePlatformTenant) ||
		!validPlatformTenantWebhookFilter(in.EventFilter) {
		return AppWebhook{}, ErrInvalidAppWebhookScope
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tenant, ok := m.platformTenants[in.PlatformTenantID]
	if !ok || tenant.AccountID != in.AccountID {
		return AppWebhook{}, ErrNotFound
	}
	count := 0
	for _, hook := range m.appWebhooks {
		if hook.AccountID != in.AccountID {
			continue
		}
		if hook.Scope == AppWebhookScopePlatformTenant && hook.PlatformTenantID == in.PlatformTenantID && hook.TargetURL == in.TargetURL {
			return AppWebhook{}, ErrConflict
		}
		if hook.Scope == AppWebhookScopeAccount || hook.Scope == AppWebhookScopePlatformTenant {
			count++
			continue
		}
		if app, exists := m.apps[hook.AppID]; exists && app.Status != AppDeleted && app.AccountID == in.AccountID {
			count++
		}
	}
	if count >= limits.WebhookPerAccount {
		return AppWebhook{}, &AppWebhookQuotaError{Scope: AppWebhookQuotaScopeAccount, Limit: limits.WebhookPerAccount, Observed: count}
	}
	if in.ID == "" {
		in.ID = newID()
	}
	in.Scope = AppWebhookScopePlatformTenant
	in.EventFilter = append([]string(nil), in.EventFilter...)
	if in.RetryPolicy == "" {
		in.RetryPolicy = AppWebhookRetryDefault
	}
	if in.DeliveryFormat == "" {
		in.DeliveryFormat = AppWebhookDeliveryFormatJSON
	}
	if in.CreatedAt.IsZero() {
		in.CreatedAt = time.Now().UTC()
	}
	in.UpdatedAt = in.CreatedAt
	m.appWebhooks[in.ID] = in
	return in, nil
}

func (m *MemStore) ListPlatformTenantWebhookDeliveries(_ context.Context, accountID, tenantID, webhookID string, pageSize int, pageToken string) ([]AppWebhookDelivery, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	hook, ok := m.appWebhooks[webhookID]
	if !ok || hook.Scope != AppWebhookScopePlatformTenant || hook.AccountID != accountID || hook.PlatformTenantID != tenantID {
		return []AppWebhookDelivery{}, "", nil
	}
	out := make([]AppWebhookDelivery, 0)
	for _, delivery := range m.appWebhookDeliveries {
		if delivery.WebhookID == webhookID && delivery.AccountID == accountID {
			out = append(out, delivery)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		left, right := out[i].CreatedAt.UTC(), out[j].CreatedAt.UTC()
		if left.Equal(right) {
			return out[i].ID > out[j].ID
		}
		return left.After(right)
	})
	if pageToken != "" {
		ts, id, valid := decodePageToken(pageToken)
		if !valid {
			return nil, "", fmt.Errorf("state: invalid page token")
		}
		filtered := out[:0]
		for _, delivery := range out {
			if delivery.CreatedAt.Before(ts) || (delivery.CreatedAt.Equal(ts) && delivery.ID < id) {
				filtered = append(filtered, delivery)
			}
		}
		out = filtered
	}
	if pageSize <= 0 {
		pageSize = 50
	}
	if len(out) > pageSize {
		last := out[pageSize-1]
		return out[:pageSize], encodePageToken(last.CreatedAt, last.ID), nil
	}
	return out, "", nil
}
