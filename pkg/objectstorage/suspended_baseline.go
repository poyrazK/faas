package objectstorage

// SuspendedBaselineProvider reports a never-versioned bucket's versioning as
// Suspended, so Gregale cannot tell it from one whose versioning was turned
// off and must account every version (ADR-628, ADR-20261023223000). Such a
// bucket starts in all-version accounting while it is provisioning and empty
// (ADR-20261107214517).
type SuspendedBaselineProvider interface {
	ReportsNeverVersionedAsSuspended() bool
}

// StartsInAllVersionAccounting reports whether p's new buckets start in
// all-version accounting.
func StartsInAllVersionAccounting(p any) bool {
	s, ok := p.(SuspendedBaselineProvider)
	return ok && s.ReportsNeverVersionedAsSuspended()
}

// ReportsNeverVersionedAsSuspended is true: GCS has no never-enabled
// versioning status (ADR-628).
func (p *GCS) ReportsNeverVersionedAsSuspended() bool { return true }

var _ SuspendedBaselineProvider = (*GCS)(nil)
