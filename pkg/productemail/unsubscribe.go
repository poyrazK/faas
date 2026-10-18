package productemail

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// A separate, random per-delivery capability authorizes only opt-out. Its hash
// supports lookup; its sealed form keeps retry bodies stable without persisting
// a plaintext secret or borrowing an Auth session/recipient grant.
func NewUnsubscribeToken() (string, error) {
	var raw [api.ProductControlEmailUnsubscribeTokenBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}
func UnsubscribeHash(token string) ([]byte, error) {
	if len(token) != base64.RawURLEncoding.EncodedLen(api.ProductControlEmailUnsubscribeTokenBytes) {
		return nil, ErrInvalid
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(raw) != api.ProductControlEmailUnsubscribeTokenBytes {
		return nil, ErrInvalid
	}
	sum := sha256.Sum256(raw)
	return sum[:], nil
}
func UnsubscribeSealID(id string) string { return "product-email-unsubscribe/v1/" + id }
func validUnsubscribeMaterial(hash, sealed []byte) bool {
	return len(hash) == sha256.Size && len(sealed) > 0 && len(sealed) <= api.ProductControlEmailCipherMaxBytes
}
func unsubscribeReceiptID(sub string, version int64) string {
	raw, _ := json.Marshal(struct {
		Subscription string
		Version      int64
	}{sub, version})
	return uuid.NewSHA1(uuid.NameSpaceOID, append([]byte("product-email-opt-out/v1/"), raw...)).String()
}
func unsubscribe(old Subscription, d Delivery, now time.Time) (Subscription, api.ProductEmailOptOutReceipt, error) {
	var v api.ProductEmailOptOutReceipt
	if old.Version < 1 || old.Version != d.Version || old.Generation != d.Generation || !old.Work && !old.Help {
		return old, v, ErrConflict
	}
	v = api.ProductEmailOptOutReceipt{ID: unsubscribeReceiptID(old.ID(), old.Version), RecipientID: old.ID(), TenantID: old.TenantID, DeliveryID: d.ID, ExpectedVersion: old.Version, Version: min(old.Version+1, api.ProductControlNotificationMaxUpdates), Generation: old.Generation, UnsubscribedAt: now}
	// Even at the preference-version ceiling, stopping email must remain possible.
	// Preserve complaint/bounce holds and encrypted address evidence unchanged.
	next := clone(old)
	next.Version, next.Work, next.Help = v.Version, false, false
	next.Grant, next.ExpiresAt, next.Cursor = nil, time.Time{}, ""
	next.WorkSince, next.HelpSince = now, now
	return next, v, nil
}
func unsubscribeResult(v api.ProductEmailOptOutReceipt, s Subscription) api.ProductEmailUnsubscribeResult {
	return api.ProductEmailUnsubscribeResult{ReceiptID: v.ID, UnsubscribedAt: v.UnsubscribedAt, Current: s.Version == v.Version && s.Generation == v.Generation && !s.Work && !s.Help}
}
func optOutScope(recipient string) string { return "email-opt-outs/" + recipient }
func encodeOptOutCursor(s OwnerScope, recipient string, v api.ProductEmailOptOutReceipt) string {
	raw, _ := json.Marshal(ownerCursor{Scope: ownerScopeHash(s, optOutScope(recipient)), At: v.UnsubscribedAt, ID: v.ID})
	return base64.RawURLEncoding.EncodeToString(raw)
}
func decodeOptOutCursor(s OwnerScope, recipient, value string) (ownerCursor, error) {
	var v ownerCursor
	if !s.valid() || recipient != "" && !canonicalID(recipient) || len(value) > api.ProductControlOwnerCursorMaxBytes {
		return v, ErrInvalid
	}
	if value == "" {
		return v, nil
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil || json.Unmarshal(raw, &v) != nil || v.Scope != ownerScopeHash(s, optOutScope(recipient)) || !canonicalID(v.ID) || v.At.IsZero() || v.At.Year() < 1 || v.At.Year() > 9999 {
		return v, ErrInvalid
	}
	canonical, _ := json.Marshal(v)
	if string(canonical) != string(raw) {
		return v, ErrInvalid
	}
	return v, nil
}
func newOptOutPage(s OwnerScope, recipient string, now time.Time) api.ProductEmailOptOutPage {
	return api.ProductEmailOptOutPage{AccountID: s.AccountID, AppID: s.AppID, TenantID: s.TenantID, RecipientID: recipient, ObservedAt: now, WindowStart: now.Add(-api.ProductControlNotificationWindow), Receipts: []api.ProductEmailOptOutReceipt{}}
}
func finishOptOutPage(s OwnerScope, p *api.ProductEmailOptOutPage) {
	if len(p.Receipts) > api.ProductControlEmailOwnerPageSize {
		p.Receipts = p.Receipts[:api.ProductControlEmailOwnerPageSize]
		p.NextCursor = encodeOptOutCursor(s, p.RecipientID, p.Receipts[len(p.Receipts)-1])
	}
}
func ValidateOptOutPage(p api.ProductEmailOptOutPage, tenant, cursor string) error {
	s := OwnerScope{AccountID: p.AccountID, AppID: p.AppID, TenantID: tenant}
	before, err := decodeOptOutCursor(s, p.RecipientID, cursor)
	if err != nil || p.TenantID != tenant || p.ObservedAt.IsZero() || !p.WindowStart.Equal(p.ObservedAt.Add(-api.ProductControlNotificationWindow)) || p.Receipts == nil || len(p.Receipts) > api.ProductControlEmailOwnerPageSize {
		return ErrConflict
	}
	for _, v := range p.Receipts {
		if !canonicalID(v.ID) || !canonicalID(v.RecipientID) || !canonicalID(v.TenantID) || !canonicalID(v.DeliveryID) || tenant != "" && v.TenantID != tenant || p.RecipientID != "" && v.RecipientID != p.RecipientID || v.ExpectedVersion < 1 || v.ExpectedVersion > api.ProductControlNotificationMaxUpdates || v.Version != min(v.ExpectedVersion+1, api.ProductControlNotificationMaxUpdates) || v.Generation < 1 || v.Generation > api.ProductControlNotificationMaxUpdates || !v.UnsubscribedAt.After(p.WindowStart) || v.UnsubscribedAt.After(p.ObservedAt) {
			return ErrConflict
		}
		if before.ID != "" && (v.UnsubscribedAt.After(before.At) || v.UnsubscribedAt.Equal(before.At) && v.ID >= before.ID) {
			return ErrConflict
		}
		before = ownerCursor{At: v.UnsubscribedAt, ID: v.ID}
	}
	if p.NextCursor != "" && (len(p.Receipts) != api.ProductControlEmailOwnerPageSize || p.NextCursor != encodeOptOutCursor(s, p.RecipientID, p.Receipts[len(p.Receipts)-1])) {
		return ErrConflict
	}
	return nil
}
