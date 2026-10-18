package state

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

func notificationReadPrefix(s ProductNotificationScope) string {
	return s.AccountID + "/" + s.AppID + "/" + s.TenantID + "/"
}
func notificationReadKey(s ProductNotificationScope, id string) string {
	return notificationReadPrefix(s) + s.AuthAppID + "/" + s.OrganizationID + "/" + id
}

func (m *MemStore) notificationSourceLocked(s ProductNotificationScope, v productHelpView, key string, now time.Time) (productNotificationSource, bool) {
	source, ok := m.notificationActivitySourceLocked(s, v, key, now)
	return source, ok && source.value.OccurredAt.After(now.Add(-api.ProductControlNotificationWindow))
}

func (m *MemStore) notificationActivitySourceLocked(s ProductNotificationScope, v productHelpView, key string, now time.Time) (productNotificationSource, bool) {
	kind, id, ok := productcontrol.NotificationReference(key)
	if !ok {
		return productNotificationSource{}, false
	}
	var source productNotificationSource
	if kind == "help" {
		r, exists := m.productLocked().help[id]
		if !exists || r.value.Version <= 1 || !helpRowVisible(ProductHelpScope(s), r, v.permissions) {
			return source, false
		}
		label := "help_replied"
		if r.value.Status == "resolved" {
			label = "help_resolved"
		}
		source = notificationSource(label, id, "", r.value.Request.Kind, "help:"+strconv.FormatInt(r.value.Version, 10), r.value.UpdatedAt)
	} else {
		r, exists := m.productLocked().receipts[id]
		if !exists || !v.permissions.ViewWork || r.account != s.AccountID || r.app != s.AppID || r.tenant != s.TenantID {
			return source, false
		}
		if r.resolvedAt != nil && r.resolvedState != "" {
			source = notificationSource("work_"+string(r.resolvedState), id, r.receipt.Feature, "", notificationWorkVersion(string(r.resolvedState), *r.resolvedAt), *r.resolvedAt)
		} else {
			op, exists := m.operationMemoryLocked().operations[id]
			if !exists || op.AccountID != s.AccountID || op.AppID != s.AppID || op.PlatformTenantID != s.TenantID || op.State != api.OperationRequiresReconciliation || !op.ExpiresAt.After(now) {
				return source, false
			}
			source = notificationSource("work_attention", id, r.receipt.Feature, "", notificationWorkVersion(string(op.State), op.UpdatedAt), op.UpdatedAt)
		}
	}
	return source, true
}

func (m *MemStore) ListProductNotifications(ctx context.Context, s ProductNotificationScope, filter, raw string) (api.ProductNotificationPage, error) {
	cursor, err := notificationCursor(s, filter, raw)
	if err != nil {
		return api.ProductNotificationPage{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	v, err := m.helpViewLocked(ProductHelpScope(s))
	if err != nil {
		return api.ProductNotificationPage{}, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	page := notificationPage(s, now)
	visit := func(key string) {
		source, ok := m.notificationSourceLocked(s, v, key, now)
		if !ok {
			return
		}
		n := notificationProjection(s, source, m.productLocked().notificationReads[notificationReadKey(s, key)], now)
		if !n.Read {
			page.UnreadCount++
		}
		if filter == "unread" && n.Read || cursor.ID != "" && (n.OccurredAt.After(cursor.At) || n.OccurredAt.Equal(cursor.At) && n.ID >= cursor.ID) {
			return
		}
		page.Items = append(page.Items, n)
		sort.Slice(page.Items, func(i, j int) bool {
			if page.Items[i].OccurredAt.Equal(page.Items[j].OccurredAt) {
				return page.Items[i].ID > page.Items[j].ID
			}
			return page.Items[i].OccurredAt.After(page.Items[j].OccurredAt)
		})
		if len(page.Items) > api.ProductControlNotificationPageSize+1 {
			page.Items = page.Items[:api.ProductControlNotificationPageSize+1]
		}
	}
	for id := range m.productLocked().receipts {
		visit("work/" + id)
	}
	for id := range m.productLocked().help {
		visit("help/" + id)
	}
	return notificationFinish(page, cursor), ctx.Err()
}

func (m *MemStore) UpdateProductNotification(ctx context.Context, s ProductNotificationScope, req api.ProductNotificationUpdate) (api.ProductNotification, error) {
	if productcontrol.ValidateNotificationUpdate(req) != nil {
		return api.ProductNotification{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	v, err := m.helpViewLocked(ProductHelpScope(s))
	if err != nil {
		return api.ProductNotification{}, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	source, ok := m.notificationSourceLocked(s, v, req.ID, now)
	if !ok {
		return api.ProductNotification{}, ErrNotFound
	}
	d := m.productLocked()
	key := notificationReadKey(s, req.ID)
	current := notificationProjection(s, source, d.notificationReads[key], now)
	value, changed, err := notificationUpdate(current, req)
	if err != nil {
		return value, err
	}
	if err = ctx.Err(); err != nil || !changed {
		return value, err
	}
	count := 0
	for k, r := range d.notificationReads {
		if strings.HasPrefix(k, notificationReadPrefix(s)) {
			if !r.expires.After(now) {
				delete(d.notificationReads, k)
			} else {
				count++
			}
		}
	}
	if current.ReadVersion == 0 && count >= api.ProductControlNotificationMaxReads {
		return current, NewOperationLimitError("product_notification_reads", api.ProductControlNotificationMaxReads, int64(count+1))
	}
	if d.notificationReads == nil {
		d.notificationReads = map[string]productNotificationRead{}
	}
	d.notificationReads[key] = productNotificationRead{sourceVersion: source.version, read: value.Read, version: value.ReadVersion, expires: source.value.OccurredAt.Add(api.ProductControlNotificationWindow)}
	return value, nil
}

// GetProductNotification rechecks source retention, mapping, realm and current
// role permissions immediately before an email can leave the outbox.
func (m *MemStore) GetProductNotification(ctx context.Context, s ProductNotificationScope, id string) (api.ProductNotification, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, err := m.helpViewLocked(ProductHelpScope(s))
	if err != nil {
		return api.ProductNotification{}, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	source, ok := m.notificationSourceLocked(s, v, id, now)
	if !ok {
		return api.ProductNotification{}, ErrNotFound
	}
	return notificationProjection(s, source, productNotificationRead{}, now), ctx.Err()
}
