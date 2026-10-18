package productemail

import (
	"context"
	"sort"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *Memory) OwnerPage(ctx context.Context, scope OwnerScope, status, cursor string, now time.Time) (api.ProductEmailOwnerPage, error) {
	page := newOwnerPage(scope, status, now)
	after, err := decodeOwnerCursor(scope, status, cursor)
	if err != nil {
		return page, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.deliveries {
		sub := m.subscriptions[d.SubscriptionID]
		if !scope.includes(sub) || !d.CreatedAt.After(page.WindowStart) {
			continue
		}
		switch d.Status {
		case "queued":
			page.Counts.Queued++
		case "leased":
			page.Counts.Leased++
		case "captured":
			page.Counts.Captured++
		case "provider_accepted":
			page.Counts.ProviderAccepted++
		case "failed":
			page.Counts.Failed++
		case "cancelled":
			page.Counts.Cancelled++
		case "delivered":
			page.Counts.Delivered++
		case "bounced":
			page.Counts.Bounced++
		case "complained":
			page.Counts.Complained++
		}
		if (d.Status == "queued" || d.Status == "leased") && (page.OldestPendingAt == nil || d.CreatedAt.Before(*page.OldestPendingAt)) {
			at := d.CreatedAt
			page.OldestPendingAt = &at
		}
		if status != "all" && d.Status != status {
			continue
		}
		if after.ID != "" && (d.CreatedAt.After(after.At) || d.CreatedAt.Equal(after.At) && d.ID >= after.ID) {
			continue
		}
		item := ownerDelivery(sub, d, now)
		i := sort.Search(len(page.Deliveries), func(i int) bool { return ownerBefore(item, page.Deliveries[i]) })
		if i > api.ProductControlEmailOwnerPageSize {
			continue
		}
		page.Deliveries = append(page.Deliveries, api.ProductEmailOwnerDelivery{})
		copy(page.Deliveries[i+1:], page.Deliveries[i:])
		page.Deliveries[i] = item
		if len(page.Deliveries) > api.ProductControlEmailOwnerPageSize+1 {
			page.Deliveries = page.Deliveries[:api.ProductControlEmailOwnerPageSize+1]
		}
	}
	finishOwnerPage(scope, &page)
	return page, ctx.Err()
}
func ownerBefore(a, b api.ProductEmailOwnerDelivery) bool {
	if a.CreatedAt.Equal(b.CreatedAt) {
		return a.ID > b.ID
	}
	return a.CreatedAt.After(b.CreatedAt)
}
func (m *Memory) ownerGet(scope OwnerScope, id string, now time.Time) (Subscription, Delivery, error) {
	d, ok := m.deliveries[id]
	sub := m.subscriptions[d.SubscriptionID]
	if !ok || !scope.includes(sub) || !d.CreatedAt.After(now.Add(-api.ProductControlNotificationWindow)) {
		return Subscription{}, Delivery{}, ErrNotFound
	}
	return clone(sub), cloneDelivery(d), nil
}
func (m *Memory) OwnerGet(ctx context.Context, scope OwnerScope, id string, now time.Time) (Subscription, Delivery, error) {
	if !scope.valid() || scope.TenantID == "" || !canonicalID(id) {
		return Subscription{}, Delivery{}, ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Subscription{}, Delivery{}, err
	}
	return m.ownerGet(scope, id, now)
}
func retryKey(id string, attempt int) string { return id + "/" + strconv.Itoa(attempt) }
func (m *Memory) RetryReceipt(ctx context.Context, scope OwnerScope, id string, attempt int, now time.Time) (api.ProductEmailRetryReceipt, error) {
	if !scope.valid() || scope.TenantID == "" || !canonicalID(id) || attempt < 1 || attempt >= api.ProductControlEmailMaxAttempts {
		return api.ProductEmailRetryReceipt{}, ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, _, err := m.ownerGet(scope, id, now); err != nil {
		return api.ProductEmailRetryReceipt{}, err
	}
	r, ok := m.retries[retryKey(id, attempt)]
	if !ok {
		return r, ErrNotFound
	}
	return r, ctx.Err()
}
func (m *Memory) Retry(ctx context.Context, scope OwnerScope, d Delivery, now time.Time) (api.ProductEmailRetryReceipt, error) {
	if !scope.valid() || scope.TenantID == "" || !canonicalID(d.ID) || d.Attempts < 1 || d.Attempts >= api.ProductControlEmailMaxAttempts {
		return api.ProductEmailRetryReceipt{}, ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return api.ProductEmailRetryReceipt{}, err
	}
	sub, current, err := m.ownerGet(scope, d.ID, now)
	if err != nil {
		return api.ProductEmailRetryReceipt{}, err
	}
	key := retryKey(d.ID, d.Attempts)
	if r, ok := m.retries[key]; ok {
		return r, nil
	}
	if current.Attempts != d.Attempts || current.Version != d.Version || current.Notification.Revision != d.Notification.Revision || RetryBlocked(sub, current, now) != "" {
		return api.ProductEmailRetryReceipt{}, ErrConflict
	}
	current.Status, current.Reason, current.NextAt, current.UpdatedAt = "queued", "", now, now
	m.deliveries[d.ID] = current
	r := api.ProductEmailRetryReceipt{DeliveryID: d.ID, TenantID: sub.TenantID, FailedAttempt: d.Attempts, QueuedAt: now}
	m.retries[key] = r
	return r, nil
}
