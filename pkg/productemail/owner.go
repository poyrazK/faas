package productemail

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type OwnerScope struct{ AccountID, AppID, TenantID string }

func canonicalID(v string) bool {
	id, err := uuid.Parse(v)
	return err == nil && id != uuid.Nil && id.String() == v
}
func (s OwnerScope) valid() bool {
	// MemStore uses compact UUIDs for existing platform accounts and apps.
	return platformID(s.AccountID) && platformID(s.AppID) && (s.TenantID == "" || canonicalID(s.TenantID))
}
func platformID(v string) bool {
	id, err := uuid.Parse(v)
	return err == nil && id != uuid.Nil && (id.String() == v || strings.ReplaceAll(id.String(), "-", "") == v)
}
func (s OwnerScope) includes(sub Subscription) bool {
	return s.AccountID == sub.AccountID && s.AppID == sub.AppID && (s.TenantID == "" || s.TenantID == sub.TenantID)
}
func OwnerStatus(v string) bool {
	return v == "all" || v == "queued" || v == "leased" || v == "captured" || v == "provider_accepted" || v == "delivered" || v == "bounced" || v == "complained" || v == "failed" || v == "cancelled"
}

type ownerCursor struct {
	Scope string
	At    time.Time
	ID    string
}

func ownerScopeHash(s OwnerScope, status string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{"product-email-owner/v1", s.AccountID, s.AppID, s.TenantID, status}, "/")))
	return hex.EncodeToString(sum[:])
}
func encodeOwnerCursor(s OwnerScope, status string, d api.ProductEmailOwnerDelivery) string {
	raw, _ := json.Marshal(ownerCursor{Scope: ownerScopeHash(s, status), At: d.CreatedAt, ID: d.ID})
	return base64.RawURLEncoding.EncodeToString(raw)
}
func decodeOwnerCursor(s OwnerScope, status, value string) (ownerCursor, error) {
	var out ownerCursor
	if !s.valid() || !OwnerStatus(status) || len(value) > api.ProductControlOwnerCursorMaxBytes {
		return out, ErrInvalid
	}
	if value == "" {
		return out, nil
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil || json.Unmarshal(raw, &out) != nil || out.Scope != ownerScopeHash(s, status) || !canonicalID(out.ID) || out.At.IsZero() || out.At.Year() < 1 || out.At.Year() > 9999 {
		return out, ErrInvalid
	}
	canonical, _ := json.Marshal(out)
	if string(canonical) != string(raw) {
		return out, ErrInvalid
	}
	return out, nil
}
func newOwnerPage(s OwnerScope, status string, now time.Time) api.ProductEmailOwnerPage {
	return api.ProductEmailOwnerPage{AccountID: s.AccountID, AppID: s.AppID, TenantID: s.TenantID, Status: status, ObservedAt: now, WindowStart: now.Add(-api.ProductControlNotificationWindow), Deliveries: []api.ProductEmailOwnerDelivery{}}
}
func finishOwnerPage(s OwnerScope, page *api.ProductEmailOwnerPage) {
	if len(page.Deliveries) > api.ProductControlEmailOwnerPageSize {
		page.Deliveries = page.Deliveries[:api.ProductControlEmailOwnerPageSize]
		page.NextCursor = encodeOwnerCursor(s, page.Status, page.Deliveries[len(page.Deliveries)-1])
	}
}

// RetryBlocked is a queue eligibility check. Current Auth role, mapping and
// source checks belong to the service and run again before an external send.
func RetryBlocked(sub Subscription, d Delivery, now time.Time) string {
	if sub.PauseReason != "" {
		return "recipient_suppressed"
	}
	if d.Status != "failed" || d.Reason != "provider_rejected" {
		return "not_provider_rejected"
	}
	if d.Attempts < 1 || d.Attempts >= api.ProductControlEmailMaxAttempts {
		return "attempts_exhausted"
	}
	if !d.Deadline.After(now) {
		return "expired"
	}
	if d.NextAt.After(now) {
		return "backoff_active"
	}
	if sub.Version != d.Version || !Enabled(sub, d.Notification, now) {
		return "preferences_changed"
	}
	return ""
}
func ownerDelivery(sub Subscription, d Delivery, now time.Time) api.ProductEmailOwnerDelivery {
	d = cloneDelivery(d)
	reason := RetryBlocked(sub, d, now)
	v := api.ProductEmailOwnerDelivery{ProductEmailDelivery: d.ProductEmailDelivery, TenantID: sub.TenantID, RecipientID: d.SubscriptionID, NextAttemptAt: d.NextAt, Deadline: d.Deadline, RetryEligible: reason == "", RetryBlockedReason: reason, PauseReason: sub.PauseReason}
	if !sub.PausedAt.IsZero() {
		at := sub.PausedAt
		v.PausedAt = &at
	}
	return v
}
