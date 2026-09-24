package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) CreateAccountReleaseWebhookIfUnderQuota(_ context.Context, in AppWebhook, limits api.Limits) (AppWebhook, error) {
	if in.AppID != "" || (in.Scope != "" && in.Scope != AppWebhookScopeAccount) ||
		!validAccountReleaseWebhookFilter(in.EventFilter) {
		return AppWebhook{}, ErrInvalidAppWebhookScope
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[in.AccountID]; !ok {
		return AppWebhook{}, ErrNotFound
	}
	count := 0
	for _, hook := range m.appWebhooks {
		if hook.AccountID != in.AccountID {
			continue
		}
		if hook.Scope == AppWebhookScopeAccount {
			if hook.TargetURL == in.TargetURL {
				return AppWebhook{}, ErrConflict
			}
			count++
			continue
		}
		if app, ok := m.apps[hook.AppID]; ok && app.Status != AppDeleted && app.AccountID == in.AccountID {
			count++
		}
	}
	if count >= limits.WebhookPerAccount {
		return AppWebhook{}, &AppWebhookQuotaError{
			Scope: AppWebhookQuotaScopeAccount, Limit: limits.WebhookPerAccount, Observed: count,
		}
	}
	if in.ID == "" {
		in.ID = newID()
	}
	in.Scope = AppWebhookScopeAccount
	in.EventFilter = append([]string(nil), in.EventFilter...)
	if in.RetryPolicy == "" {
		in.RetryPolicy = AppWebhookRetryDefault
	}
	if in.DeliveryFormat == "" {
		in.DeliveryFormat = AppWebhookDeliveryFormatJSON
	}
	if in.CreatedAt.IsZero() {
		in.CreatedAt = time.Now()
	}
	in.UpdatedAt = in.CreatedAt
	m.appWebhooks[in.ID] = in
	return in, nil
}

func (m *MemStore) ListAccountReleaseWebhookDeliveries(_ context.Context, accountID, webhookID string, pageSize int, pageToken string) ([]AppWebhookDelivery, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	hook, ok := m.appWebhooks[webhookID]
	if !ok || hook.Scope != AppWebhookScopeAccount || hook.AccountID != accountID {
		return []AppWebhookDelivery{}, "", nil
	}
	out := make([]AppWebhookDelivery, 0)
	for _, delivery := range m.appWebhookDeliveries {
		if delivery.WebhookID == webhookID && delivery.AccountID == accountID {
			out = append(out, delivery)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if pageToken != "" {
		ts, id, ok := decodePageToken(pageToken)
		if !ok {
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
		next := encodePageToken(last.CreatedAt, last.ID)
		out = out[:pageSize]
		return out, next, nil
	}
	return out, "", nil
}
