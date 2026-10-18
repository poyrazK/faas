package productemail

import (
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// ValidateOwnerPage prevents consumers from acting on a mismatched or malformed
// queue response. Retry eligibility remains advisory until the server rechecks it.
func ValidateOwnerPage(p api.ProductEmailOwnerPage, tenant, status, cursor string) error {
	scope := OwnerScope{AccountID: p.AccountID, AppID: p.AppID, TenantID: tenant}
	if !scope.valid() || p.TenantID != tenant || p.Status != status || !OwnerStatus(status) || p.ObservedAt.IsZero() || !p.WindowStart.Equal(p.ObservedAt.Add(-api.ProductControlNotificationWindow)) || p.Deliveries == nil || len(p.Deliveries) > api.ProductControlEmailOwnerPageSize || (p.Mode != "disabled" && p.Mode != "local" && p.Mode != "resend") {
		return ErrConflict
	}
	before, err := decodeOwnerCursor(scope, status, cursor)
	if err != nil {
		return ErrConflict
	}
	c := p.Counts
	if c.Queued < 0 || c.Leased < 0 || c.Captured < 0 || c.ProviderAccepted < 0 || c.Failed < 0 || c.Cancelled < 0 || c.Delivered < 0 || c.Bounced < 0 || c.Complained < 0 {
		return ErrConflict
	}
	if (c.Queued+c.Leased > 0) != (p.OldestPendingAt != nil) {
		return ErrConflict
	}
	if p.OldestPendingAt != nil && (!p.OldestPendingAt.After(p.WindowStart) || p.OldestPendingAt.After(p.ObservedAt)) {
		return ErrConflict
	}
	seen := map[string]bool{}
	for _, d := range p.Deliveries {
		if !canonicalID(d.ID) || !canonicalID(d.TenantID) || !canonicalID(d.RecipientID) || tenant != "" && d.TenantID != tenant || !OwnerStatus(d.Status) || d.Status == "all" || status != "all" && d.Status != status || d.Attempts < 0 || d.Attempts > api.ProductControlEmailMaxAttempts || !d.CreatedAt.After(p.WindowStart) || d.UpdatedAt.Before(d.CreatedAt) || !d.Deadline.Equal(d.CreatedAt.Add(api.ProductControlEmailRetryWindow)) || d.NextAttemptAt.IsZero() || seen[d.ID] || !validReason(d.Reason) {
			return ErrConflict
		}
		if !ownerNotification(d.NotificationID, d.Kind) {
			return ErrConflict
		}
		reported := d.Status == "delivered" || d.Status == "bounced" || d.Status == "complained"
		if reported != (d.ReportedAt != nil) || d.ReportedAt != nil && d.ReportedAt.IsZero() || d.PauseReason != "" && d.PauseReason != "hard_bounce" && d.PauseReason != "complaint" || d.RetryEligible && d.PauseReason != "" {
			return ErrConflict
		}
		if before.ID != "" && (d.CreatedAt.After(before.At) || d.CreatedAt.Equal(before.At) && d.ID >= before.ID) {
			return ErrConflict
		}
		if d.RetryEligible && (p.Mode == "disabled" || d.Status != "failed" || d.Reason != "provider_rejected" || d.Attempts < 1 || d.Attempts >= api.ProductControlEmailMaxAttempts || !d.Deadline.After(p.ObservedAt) || d.NextAttemptAt.After(p.ObservedAt) || d.RetryBlockedReason != "") {
			return ErrConflict
		}
		seen[d.ID] = true
		before = ownerCursor{At: d.CreatedAt, ID: d.ID}
	}
	if p.NextCursor != "" {
		if len(p.Deliveries) != api.ProductControlEmailOwnerPageSize || p.NextCursor != encodeOwnerCursor(scope, status, p.Deliveries[len(p.Deliveries)-1]) {
			return ErrConflict
		}
	}
	return nil
}
func ownerNotification(id, kind string) bool {
	prefix := "work/"
	switch kind {
	case "work_succeeded", "work_failed", "work_cancelled", "work_attention":
	case "help_replied", "help_resolved":
		prefix = "help/"
	default:
		return false
	}
	return strings.HasPrefix(id, prefix) && canonicalID(strings.TrimPrefix(id, prefix))
}
