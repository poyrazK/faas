// MemStore implementations for issue #476 (ADR-076) — outbound
// webhook subscriptions + delivery ledger. Kept in a separate file
// from memstore.go to keep the original file under ~9000 lines and
// to surface the new surface area at a glance.
//
// Mirrors the alert_rules + alert_deliveries MemStore pattern
// (memstore.go:7513-7834): the per-app + per-account quota gate is
// mirrored in Go because there are no SQL indexes backing the
// in-memory store; the unique (app_id, target_url) invariant is
// enforced at insert time; the dispatcher's claim query is a
// single goroutine today so the in-memory store does not need a
// per-row lock.
package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// CreateAppWebhook rejects on duplicate (app_id, target_url) before
// insert — same invariant the Postgres unique index holds.
func (m *MemStore) CreateAppWebhook(_ context.Context, in AppWebhook) (AppWebhook, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.appWebhooks {
		if existing.AppID == in.AppID && existing.TargetURL == in.TargetURL {
			return AppWebhook{}, ErrConflict
		}
	}
	if in.ID == "" {
		in.ID = newID()
	}
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
	if in.EventFilter == nil {
		in.EventFilter = []string{}
	}
	m.appWebhooks[in.ID] = in
	return in, nil
}

// CreateAppWebhookIfUnderQuota enforces the per-app + per-account
// caps with the same TOCTOU-defence shape as CreateCronIfUnderQuota:
// MemStore is single-process so a single critical section (m.mu)
// gates the count + insert. Unlike alert rules, an outbound webhook
// always pins an app (no account-wide shape).
func (m *MemStore) CreateAppWebhookIfUnderQuota(_ context.Context, in AppWebhook, limits api.Limits) (AppWebhook, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.appWebhooks {
		if existing.AppID == in.AppID && existing.TargetURL == in.TargetURL {
			return AppWebhook{}, ErrConflict
		}
	}
	if in.AppID == "" {
		// app_id is required for an outbound webhook (no account-wide
		// shape, unlike alert rules).
		return AppWebhook{}, ErrNotFound
	}
	app, ok := m.apps[in.AppID]
	if !ok || app.Status == AppDeleted {
		return AppWebhook{}, ErrNotFound
	}
	appCount := 0
	for _, w := range m.appWebhooks {
		if w.AppID == in.AppID {
			appCount++
		}
	}
	if appCount >= limits.WebhookPerApp {
		return AppWebhook{}, &AppWebhookQuotaError{
			Scope:    AppWebhookQuotaScopeApp,
			Limit:    limits.WebhookPerApp,
			Observed: appCount,
		}
	}
	accountCount := 0
	for _, w := range m.appWebhooks {
		if w.AccountID != in.AccountID {
			continue
		}
		if a, ok := m.apps[w.AppID]; ok && a.Status != AppDeleted {
			accountCount++
		}
	}
	if accountCount >= limits.WebhookPerAccount {
		return AppWebhook{}, &AppWebhookQuotaError{
			Scope:    AppWebhookQuotaScopeAccount,
			Limit:    limits.WebhookPerAccount,
			Observed: accountCount,
		}
	}
	if in.ID == "" {
		in.ID = newID()
	}
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
	if in.EventFilter == nil {
		in.EventFilter = []string{}
	}
	m.appWebhooks[in.ID] = in
	return in, nil
}

func (m *MemStore) AppWebhookByID(_ context.Context, id string) (AppWebhook, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.appWebhooks[id]
	if !ok {
		return AppWebhook{}, ErrNotFound
	}
	return w, nil
}

// UpdateAppWebhook mirrors UpdateAlertRule's nil-skip semantics.
func (m *MemStore) UpdateAppWebhook(_ context.Context, id string, p UpdateAppWebhookParams) (AppWebhook, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.appWebhooks[id]
	if !ok {
		return AppWebhook{}, ErrNotFound
	}
	if p.TargetURL != nil {
		w.TargetURL = *p.TargetURL
	}
	if p.EventFilter != nil {
		w.EventFilter = append([]string(nil), *p.EventFilter...)
	}
	if p.RetryPolicy != nil {
		w.RetryPolicy = *p.RetryPolicy
	}
	if p.DeliveryFormat != nil {
		w.DeliveryFormat = *p.DeliveryFormat
	}
	if p.Enabled != nil {
		w.Enabled = *p.Enabled
	}
	if p.WebhookSecretSealed != nil {
		cp := make([]byte, len(*p.WebhookSecretSealed))
		copy(cp, *p.WebhookSecretSealed)
		w.SecretSealed = cp
	}
	w.UpdatedAt = time.Now()
	m.appWebhooks[id] = w
	return w, nil
}

func (m *MemStore) DeleteAppWebhook(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.appWebhooks[id]; !ok {
		return ErrNotFound
	}
	delete(m.appWebhooks, id)
	return nil
}

func (m *MemStore) ListAppWebhooksForApp(_ context.Context, appID string) ([]AppWebhook, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AppWebhook
	for _, w := range m.appWebhooks {
		if w.AppID == appID {
			out = append(out, w)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (m *MemStore) ListAppWebhooksForAccount(_ context.Context, accountID string) ([]AppWebhook, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AppWebhook
	for _, w := range m.appWebhooks {
		if w.AccountID == accountID {
			out = append(out, w)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// RecordAppWebhookDelivery stamps the row with a fresh ID + default
// status='pending' + attempt=0 + next_attempt_at=now if unset. The
// dispatcher picks it up at next tick.
func (m *MemStore) RecordAppWebhookDelivery(_ context.Context, in AppWebhookDelivery) (AppWebhookDelivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.appWebhooks[in.WebhookID]; !ok {
		return AppWebhookDelivery{}, ErrNotFound
	}
	if in.ID == "" {
		in.ID = newID()
	}
	if in.Status == "" {
		in.Status = AppWebhookDeliveryPending
	}
	if in.NextAttemptAt.IsZero() {
		in.NextAttemptAt = time.Now()
	}
	if in.CreatedAt.IsZero() {
		in.CreatedAt = time.Now()
	}
	in.UpdatedAt = in.CreatedAt
	m.appWebhookDeliveries[in.ID] = in
	return in, nil
}

// ClaimDueAppWebhookDeliveries mirrors the PgStore's claim
// transaction shape: per-account round-robin (ORDER BY
// account_id, next_attempt_at), status='pending' → 'in_flight'
// transition. In-flight rows whose next_attempt_at has passed (an
// orphaned row from a dispatcher restart) are also reclaimable —
// see the PgStore claim transaction in pgstore_app_webhooks.go.
func (m *MemStore) ClaimDueAppWebhookDeliveries(_ context.Context, limit int, now time.Time) ([]AppWebhookDelivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var candidates []AppWebhookDelivery
	for _, d := range m.appWebhookDeliveries {
		if (d.Status == AppWebhookDeliveryPending || d.Status == AppWebhookDeliveryInFlight) &&
			!d.NextAttemptAt.After(now) {
			candidates = append(candidates, d)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].AccountID != candidates[j].AccountID {
			return candidates[i].AccountID < candidates[j].AccountID
		}
		return candidates[i].NextAttemptAt.Before(candidates[j].NextAttemptAt)
	})
	if limit > 0 && len(candidates) > limit {
		candidates = candidates[:limit]
	}
	out := make([]AppWebhookDelivery, len(candidates))
	for i, d := range candidates {
		d.Status = AppWebhookDeliveryInFlight
		d.UpdatedAt = now
		m.appWebhookDeliveries[d.ID] = d
		out[i] = d
	}
	return out, nil
}

func (m *MemStore) MarkAppWebhookDeliverySucceeded(_ context.Context, id string, responseCode, currentAttempt int, deliveredAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.appWebhookDeliveries[id]
	if !ok {
		return ErrNotFound
	}
	d.Status = AppWebhookDeliverySucceeded
	d.LastResponseCode = responseCode
	d.Attempt = currentAttempt + 1
	delivered := deliveredAt
	d.DeliveredAt = &delivered
	d.NextAttemptAt = time.Time{}
	d.UpdatedAt = time.Now()
	m.appWebhookDeliveries[id] = d
	return nil
}

func (m *MemStore) MarkAppWebhookDeliveryFailed(_ context.Context, id string, responseCode, currentAttempt int, errMsg string, nextAttemptAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.appWebhookDeliveries[id]
	if !ok {
		return ErrNotFound
	}
	// Reset to 'pending' so the dispatcher's claim query
	// (`WHERE status IN ('pending','in_flight') AND next_attempt_at <= now()`)
	// picks the row up when the rescheduled time arrives. The
	// `last_response_code` + `last_error` columns preserve the
	// historical failure record; status only tracks lifecycle.
	d.Status = AppWebhookDeliveryPending
	d.LastResponseCode = responseCode
	d.LastError = errMsg
	d.Attempt = currentAttempt + 1
	d.NextAttemptAt = nextAttemptAt
	d.UpdatedAt = time.Now()
	m.appWebhookDeliveries[id] = d
	return nil
}

func (m *MemStore) MarkAppWebhookDeliveryDead(_ context.Context, id string, currentAttempt int, errMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.appWebhookDeliveries[id]
	if !ok {
		return ErrNotFound
	}
	d.Status = AppWebhookDeliveryDead
	d.LastError = errMsg
	d.Attempt = currentAttempt + 1
	d.NextAttemptAt = time.Time{}
	d.UpdatedAt = time.Now()
	m.appWebhookDeliveries[id] = d
	return nil
}

func (m *MemStore) ResetAppWebhookDeliveryFromDead(_ context.Context, id, webhookID, accountID string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.appWebhookDeliveries[id]
	if !ok {
		return ErrNotFound
	}
	// SQL-level IDOR guard — mirror the PgStore's
	// `where id = $1 and webhook_id = $2 and account_id = $3` filter.
	if d.WebhookID != webhookID || d.AccountID != accountID {
		return ErrNotFound
	}
	if d.Status != AppWebhookDeliveryDead {
		return ErrConflict
	}
	d.Status = AppWebhookDeliveryPending
	d.Attempt = 0
	d.LastError = ""
	d.NextAttemptAt = now
	d.UpdatedAt = now
	m.appWebhookDeliveries[id] = d
	return nil
}

// ListAppWebhookDeliveries mirrors ListAlertDeliveriesForRule's
// "most recent first" orientation. The MemStore implementation
// ignores pageToken (a synthetic cursor in production); tests use
// pageToken="" to read the first page. Pagination stops at
// pageSize rows.
func (m *MemStore) ListAppWebhookDeliveries(_ context.Context, appID, webhookID string, pageSize int, _ string) ([]AppWebhookDelivery, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AppWebhookDelivery
	for _, d := range m.appWebhookDeliveries {
		if d.AppID != appID || d.WebhookID != webhookID {
			continue
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if pageSize > 0 && len(out) > pageSize {
		out = out[:pageSize]
	}
	return out, "", nil
}

func (m *MemStore) AppWebhookDeliveryByID(_ context.Context, id string) (AppWebhookDelivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.appWebhookDeliveries[id]
	if !ok {
		return AppWebhookDelivery{}, ErrNotFound
	}
	return d, nil
}
