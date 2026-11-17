package tasktoolsource

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func NewRecord(account, key, id string, r Review, now time.Time) (api.HostedTaskSourceIntent, error) {
	var out api.HostedTaskSourceIntent
	raw, _ := json.Marshal(r)
	if uuid.Validate(account) != nil || uuid.Validate(key) != nil || !toolpublication.ValidID(id) || r.AccountID != account {
		return out, ErrInvalid
	}
	if _, err := Decode(raw); err != nil {
		return out, err
	}
	identity, _ := json.Marshal([]string{"hosted-task-source-intent:v1", account, key, id, r.Fingerprint})
	now = now.UTC().Truncate(time.Microsecond)
	return api.HostedTaskSourceIntent{Version: 1, ID: id, AccountID: account, RequesterKeyID: key, IntentSHA256: toolpublication.Digest(identity), ReviewSHA256: r.Fingerprint, Review: raw, CreatedAt: now, ExpiresAt: now.Add(api.HostedTaskSourceRetention), SourceState: "retained", ReviewStatus: "pending", OwnerAuthorizationRequired: true}, nil
}
func View(r api.HostedTaskSourceIntent, hasSource bool, now time.Time) api.HostedTaskSourceIntent {
	r.ReadyToApply, r.BindingsVerified, r.OwnerAuthorizationRequired = false, false, true
	if r.ReviewStatus == "" {
		r.ReviewStatus = "pending" // N-1 retained records predate owner review.
	}
	if !hasSource || !now.Before(r.ExpiresAt) {
		r.SourceState = "expired"
		if r.ReviewStatus == "pending" || r.ReviewStatus == "approved" {
			r.ReviewStatus = "expired"
		}
	}
	return r
}

// ValidateRecord prevents a malformed native response from implying authority.
func ValidateRecord(r api.HostedTaskSourceIntent, account, id string) error {
	review, err := Decode(r.Review)
	if err != nil || r.Version != 1 || r.ID != id || r.AccountID != account || r.ReviewSHA256 != review.Fingerprint || r.ReadyToApply || r.BindingsVerified || !r.OwnerAuthorizationRequired || r.SourceState != "retained" && r.SourceState != "expired" || r.CreatedAt.IsZero() || r.ExpiresAt.Sub(r.CreatedAt) != api.HostedTaskSourceRetention {
		return ErrInvalid
	}
	expected, err := NewRecord(account, r.RequesterKeyID, id, review, r.CreatedAt)
	if err != nil || expected.IntentSHA256 != r.IntentSHA256 {
		return ErrInvalid
	}
	status := r.ReviewStatus
	if status == "" {
		status = "pending"
		if r.SourceState == "expired" {
			status = "expired"
		}
	}
	switch status {
	case "pending", "approved", "rejected", "revoked", "stale", "expired":
	default:
		return ErrInvalid
	}
	if (status == "approved" || status == "rejected" || status == "revoked") && r.DecidedAt == nil || status == "pending" && r.DecidedAt != nil {
		return ErrInvalid
	}
	if r.DecidedAt != nil && (r.DecidedAt.IsZero() || r.DecidedAt.Before(r.CreatedAt) || !r.DecidedAt.Before(r.ExpiresAt)) {
		return ErrInvalid
	}
	if r.SourceState == "expired" && (status == "pending" || status == "approved") {
		return ErrInvalid
	}
	return nil
}
