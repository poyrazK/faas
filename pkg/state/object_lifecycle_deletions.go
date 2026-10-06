package state

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Lifecycle binds an ordinary deletion receipt to the rule and discovery
// identity which authorized it. Provider identities and lease tokens stay
// private. The target timestamp retains its original nanosecond precision.
type ObjectLifecycleDeletionBinding struct {
	ScanID                    string    `json:"scan_id"`
	ScanToken                 string    `json:"scan_token"`
	RuleID                    string    `json:"rule_id"`
	Kind                      string    `json:"kind"`
	ExpectedProviderVersionID string    `json:"expected_provider_version_id"`
	ExpectedLastModified      time.Time `json:"expected_last_modified"`
	ExpectedDeleteMarker      *bool     `json:"expected_delete_marker,omitempty"`
}

func cloneLifecycleDeletionBinding(b *ObjectLifecycleDeletionBinding) *ObjectLifecycleDeletionBinding {
	if b == nil {
		return nil
	}
	v := *b
	v.ExpectedLastModified = v.ExpectedLastModified.UTC()
	if b.ExpectedDeleteMarker != nil {
		marker := *b.ExpectedDeleteMarker
		v.ExpectedDeleteMarker = &marker
	}
	return &v
}

func sameLifecycleDeletionBinding(a, b *ObjectLifecycleDeletionBinding) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	// A restarted scanner may replay a receipt with a new scan lease. The
	// original token remains immutable and cannot authorize a new dispatch.
	marker := a.ExpectedDeleteMarker == nil && b.ExpectedDeleteMarker == nil || a.ExpectedDeleteMarker != nil && b.ExpectedDeleteMarker != nil && *a.ExpectedDeleteMarker == *b.ExpectedDeleteMarker
	return marker && a.ScanID == b.ScanID && a.RuleID == b.RuleID && a.Kind == b.Kind && a.ExpectedProviderVersionID == b.ExpectedProviderVersionID && a.ExpectedLastModified.Equal(b.ExpectedLastModified)
}

func validLifecycleDeletionBinding(b *ObjectLifecycleDeletionBinding, selector string) bool {
	if b == nil {
		return true
	}
	id, err := uuid.Parse(b.ScanID)
	if err != nil || id.String() != b.ScanID || id.Version() != 4 || id.Variant() != uuid.RFC4122 || !validVersionReferenceText(b.ScanToken, api.MaxObjectLifecycleTokenBytes) || !validVersionReferenceText(b.RuleID, api.MaxObjectLifecycleRuleIDRunes*utf8.UTFMax) || utf8.RuneCountInString(b.RuleID) > api.MaxObjectLifecycleRuleIDRunes || !validVersionReferenceText(b.ExpectedProviderVersionID, api.ObjectProviderVersionIDMaxBytes) || b.ExpectedLastModified.IsZero() || b.ExpectedLastModified.Year() < 1 || b.ExpectedLastModified.Year() > 9999 {
		return false
	}
	return b.Kind == "current" && selector == "" || (b.Kind == "noncurrent" || b.Kind == "expired_marker") && selector != ""
}

func lifecycleDeletionRuleAllowed(r api.ObjectLifecycleRule, key, kind string) bool {
	if r.Status != "Enabled" || !strings.HasPrefix(key, r.Filter.Prefix) {
		return false
	}
	switch kind {
	case "current":
		return r.Expiration != nil && (r.Expiration.Days != nil || r.Expiration.Date != nil)
	case "noncurrent":
		return r.NoncurrentVersionExpiration != nil
	case "expired_marker":
		return r.Expiration != nil && (r.Expiration.Days != nil || r.Expiration.Date != nil || r.Expiration.ExpiredObjectDeleteMarker != nil && *r.Expiration.ExpiredObjectDeleteMarker)
	}
	return false
}

func validateLifecycleDeletion(j ObjectDeletion, scan ObjectLifecycleScan, policy ObjectLifecyclePolicy, now time.Time) error {
	b := j.Lifecycle
	if b == nil {
		return nil
	}
	if !validLifecycleDeletionBinding(b, j.Selector) || scan.ID != b.ScanID || scan.BucketID != j.BucketID || scan.AccountID != j.AccountID || scan.AppID != j.AppID || policy.BucketID != j.BucketID || policy.Revision != scan.Revision || !validLifecycleScanLease(scan, b.ScanToken, now) || b.ExpectedLastModified.After(now) || immutableDeletion(j) && j.TargetProviderVersionID != b.ExpectedProviderVersionID || j.Selector == "null" && b.ExpectedProviderVersionID != "null" {
		return ErrConflict
	}
	for _, r := range scan.Rules {
		if r.ID == b.RuleID && lifecycleDeletionRuleAllowed(r, j.Key, b.Kind) {
			return nil
		}
	}
	return ErrConflict
}

func (m *MemStore) validateLifecycleDeletionLocked(j ObjectDeletion) error {
	if j.Lifecycle == nil {
		return nil
	}
	return validateLifecycleDeletion(j, m.objectLifecycleScans[j.Lifecycle.ScanID], m.objectLifecyclePolicies[j.BucketID], m.clock())
}
