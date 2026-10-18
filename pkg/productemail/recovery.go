package productemail

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// RecipientEvidence is sealed before persistence. The digest itself is never
// stored or returned. Lowercasing prevents case-only changes bypassing a pause.
func RecipientEvidence(address string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(address))))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
func RecipientEvidenceID(k Key) string { return k.ID() + "/recipient/v1" }
func ValidRecoveryRequest(r api.ProductEmailRecoveryRequest) bool {
	return canonicalID(r.ID) && r.ExpectedVersion > 0 && r.ExpectedVersion < api.ProductControlNotificationMaxUpdates && r.ExpectedGeneration > 0 && r.ExpectedGeneration < api.ProductControlNotificationMaxUpdates && r.WorkOutcomes != nil && r.HelpReplies != nil && (*r.WorkOutcomes || *r.HelpReplies)
}
func MatchesRecovery(v api.ProductEmailRecoveryReceipt, r api.ProductEmailRecoveryRequest) bool {
	return ValidRecoveryRequest(r) && v.ID == r.ID && v.ExpectedVersion == r.ExpectedVersion && v.PreviousGeneration == r.ExpectedGeneration && v.WorkOutcomes == *r.WorkOutcomes && v.HelpReplies == *r.HelpReplies
}
func RecoveryBlocked(s Subscription) string {
	switch s.PauseReason {
	case "complaint":
		return "complaint_review"
	case "hard_bounce":
		if len(s.PauseRecipient) == 0 {
			return "recipient_evidence_unavailable"
		}
		return "verified_replacement_required"
	default:
		return "not_paused"
	}
}
func recovered(old, next Subscription, r api.ProductEmailRecoveryRequest, now time.Time) (Subscription, api.ProductEmailRecoveryReceipt, error) {
	var v api.ProductEmailRecoveryReceipt
	if !ValidRecoveryRequest(r) {
		return next, v, ErrInvalid
	}
	if old.ID() != next.ID() || old.Version != r.ExpectedVersion || old.Generation != r.ExpectedGeneration || RecoveryBlocked(old) != "verified_replacement_required" {
		return next, v, ErrConflict
	}
	next.Work, next.Help = *r.WorkOutcomes, *r.HelpReplies
	next, err := update(old, next, r.ExpectedVersion, now)
	if err != nil {
		return next, v, err
	}
	next.Generation = old.Generation + 1
	next.PauseReason, next.PausedAt, next.PauseRecipient = "", time.Time{}, []byte{}
	next.WorkSince, next.HelpSince, next.Cursor = now, now, ""
	v = api.ProductEmailRecoveryReceipt{ID: r.ID, RecipientID: old.ID(), TenantID: old.TenantID, ExpectedVersion: old.Version, PreviousGeneration: old.Generation, Version: next.Version, Generation: next.Generation, WorkOutcomes: next.Work, HelpReplies: next.Help, RecoveredAt: now}
	return next, v, nil
}
func recoveryScopeHash(s OwnerScope, recipient string) string {
	return ownerScopeHash(s, "email-recoveries/"+recipient)
}
func encodeRecoveryCursor(s OwnerScope, recipient string, v api.ProductEmailRecoveryReceipt) string {
	raw, _ := json.Marshal(ownerCursor{Scope: recoveryScopeHash(s, recipient), At: v.RecoveredAt, ID: v.ID})
	return base64.RawURLEncoding.EncodeToString(raw)
}
func decodeRecoveryCursor(s OwnerScope, recipient, value string) (ownerCursor, error) {
	var v ownerCursor
	if !s.valid() || recipient != "" && !canonicalID(recipient) || len(value) > api.ProductControlOwnerCursorMaxBytes {
		return v, ErrInvalid
	}
	if value == "" {
		return v, nil
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil || json.Unmarshal(raw, &v) != nil || v.Scope != recoveryScopeHash(s, recipient) || !canonicalID(v.ID) || v.At.IsZero() || v.At.Year() < 1 || v.At.Year() > 9999 {
		return v, ErrInvalid
	}
	canonical, _ := json.Marshal(v)
	if string(canonical) != string(raw) {
		return v, ErrInvalid
	}
	return v, nil
}
func newRecoveryPage(s OwnerScope, recipient string, now time.Time) api.ProductEmailRecoveryPage {
	return api.ProductEmailRecoveryPage{AccountID: s.AccountID, AppID: s.AppID, TenantID: s.TenantID, RecipientID: recipient, ObservedAt: now, WindowStart: now.Add(-api.ProductControlNotificationWindow), Receipts: []api.ProductEmailRecoveryReceipt{}}
}
func finishRecoveryPage(s OwnerScope, p *api.ProductEmailRecoveryPage) {
	if len(p.Receipts) > api.ProductControlEmailOwnerPageSize {
		p.Receipts = p.Receipts[:api.ProductControlEmailOwnerPageSize]
		p.NextCursor = encodeRecoveryCursor(s, p.RecipientID, p.Receipts[len(p.Receipts)-1])
	}
}
func ValidateRecoveryPage(p api.ProductEmailRecoveryPage, tenant, cursor string) error {
	s := OwnerScope{AccountID: p.AccountID, AppID: p.AppID, TenantID: tenant}
	before, err := decodeRecoveryCursor(s, p.RecipientID, cursor)
	if err != nil || p.TenantID != tenant || p.ObservedAt.IsZero() || !p.WindowStart.Equal(p.ObservedAt.Add(-api.ProductControlNotificationWindow)) || p.Receipts == nil || len(p.Receipts) > api.ProductControlEmailOwnerPageSize {
		return ErrConflict
	}
	for _, v := range p.Receipts {
		if !canonicalID(v.ID) || !canonicalID(v.RecipientID) || !canonicalID(v.TenantID) || tenant != "" && v.TenantID != tenant || p.RecipientID != "" && v.RecipientID != p.RecipientID || v.ExpectedVersion < 1 || v.Version != v.ExpectedVersion+1 || v.PreviousGeneration < 1 || v.Generation != v.PreviousGeneration+1 || v.Generation > api.ProductControlNotificationMaxUpdates || v.Version > api.ProductControlNotificationMaxUpdates || (!v.WorkOutcomes && !v.HelpReplies) || !v.RecoveredAt.After(p.WindowStart) || v.RecoveredAt.After(p.ObservedAt) {
			return ErrConflict
		}
		if before.ID != "" && (v.RecoveredAt.After(before.At) || v.RecoveredAt.Equal(before.At) && v.ID >= before.ID) {
			return ErrConflict
		}
		before = ownerCursor{At: v.RecoveredAt, ID: v.ID}
	}
	if p.NextCursor != "" && (len(p.Receipts) != api.ProductControlEmailOwnerPageSize || p.NextCursor != encodeRecoveryCursor(s, p.RecipientID, p.Receipts[len(p.Receipts)-1])) {
		return ErrConflict
	}
	return nil
}
