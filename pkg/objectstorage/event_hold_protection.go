package objectstorage

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A release is a transition from a freshly observed ON policy. Its final date
// is computed by the provider, so recovery uses the durable observed lower
// bound rather than fabricating a date from the worker's clock.
func validateEventHoldChange(old, next api.ObjectVersionRetention, now time.Time) error {
	if !old.Valid() || !next.ValidForWrite() || next.EventHold == "" || old.EventHold != "" && old.RetainUntilDate == nil {
		return ErrUnavailable
	}
	if next.EventHold == "OFF" && next.RetainUntilDate == nil && old.EventHold != "ON" {
		return ErrConflict
	}
	if old.EventHold == "ON" {
		if old.Mode == "COMPLIANCE" && next.Mode != "COMPLIANCE" || next.RetainUntilDate != nil && next.RetainUntilDate.Before(*old.RetainUntilDate) {
			return ErrConflict
		}
		return nil
	}
	if old.RetainUntilDate != nil && old.RetainUntilDate.After(now) {
		if next.RetainUntilDate == nil || next.RetainUntilDate.Before(*old.RetainUntilDate) || old.Mode == "COMPLIANCE" && next.Mode != "COMPLIANCE" {
			return ErrConflict
		}
	}
	return nil
}

func sameEventHoldDuration(a, b *api.ObjectRetentionPeriod) bool {
	if a == nil || b == nil || !a.Valid() || !b.Valid() {
		return false
	}
	days := func(p *api.ObjectRetentionPeriod) int32 {
		if p.Days != nil {
			return *p.Days
		}
		return *p.Years * 365
	}
	return days(a) == days(b)
}

func eventHoldMatches(j state.ObjectVersionProtection, observed api.ObjectVersionRetention) bool {
	desired, baseline := j.Retention, j.EventHoldBaseline
	if desired == nil || baseline == nil || !baseline.Valid() || !observed.Valid() || desired.Mode != observed.Mode || desired.EventHold != observed.EventHold || observed.RetainUntilDate == nil {
		return false
	}
	if desired.RetainUntilDate != nil && observed.RetainUntilDate.Before(*desired.RetainUntilDate) || baseline.RetainUntilDate != nil && observed.RetainUntilDate.Before(*baseline.RetainUntilDate) {
		return false
	}
	if desired.EventHold == "ON" {
		return sameEventHoldDuration(desired.EventHoldDuration, observed.EventHoldDuration)
	}
	if desired.RetainUntilDate == nil && (baseline.EventHold != "ON" || baseline.RetainUntilDate == nil) {
		return false
	}
	// Providers may retain the duration after release or omit it. If retained,
	// it must describe the hold whose release was accepted.
	return observed.EventHoldDuration == nil || sameEventHoldDuration(baseline.EventHoldDuration, observed.EventHoldDuration)
}
