package api

import "time"

// ObjectLockPlanViolation is Object Lock protection a plan does not include
// (ADR-20261111123000 decision 8). Kind is "lock_retention_days" for
// retention, with Limit and Observed in days, or "legal_hold" for a legal
// hold on a plan without Object Lock (Limit 0, Observed 1).
type ObjectLockPlanViolation struct {
	Kind            string
	Limit, Observed int64
}

// TotalDays is the period in days, a year counting 365. Plan caps include leap
// days, so a period of whole years always fits the cap that names them.
func (p ObjectRetentionPeriod) TotalDays() int64 {
	switch {
	case p.Days != nil:
		return int64(*p.Days)
	case p.Years != nil:
		return int64(*p.Years) * 365
	}
	return 0
}

func (l ObjectStoragePlanLimits) lockDays(observed int64) *ObjectLockPlanViolation {
	if observed <= 0 || l.MaxLockRetentionDays > 0 && observed <= int64(l.MaxLockRetentionDays) {
		return nil
	}
	return &ObjectLockPlanViolation{Kind: "lock_retention_days", Limit: int64(max(l.MaxLockRetentionDays, 0)), Observed: observed}
}

// ObjectLockConfigurationViolation checks a bucket lock configuration.
// enabled reports a bucket that already has Object Lock: on a plan without
// it, such a bucket may still clear its default retention, and nothing else.
func (l ObjectStoragePlanLimits) ObjectLockConfigurationViolation(c ObjectBucketObjectLockConfiguration, enabled bool) *ObjectLockPlanViolation {
	if c.DefaultRetention == nil {
		if l.MaxLockRetentionDays <= 0 && c.Enabled && !enabled {
			return &ObjectLockPlanViolation{Kind: "lock_retention_days", Limit: 0, Observed: 1}
		}
		return nil
	}
	r := c.DefaultRetention
	if v := l.lockDays(max((ObjectRetentionPeriod{Days: r.Days, Years: r.Years}).TotalDays(), 1)); v != nil {
		return v
	}
	if r.DefaultEventHold != nil {
		return l.lockDays(r.DefaultEventHold.TotalDays())
	}
	return nil
}

// ObjectRetentionViolation checks a version retention request at now. A
// retain-until date counts its whole days from now; an event-hold duration
// counts its period. Clearing retention or releasing an event hold is always
// allowed.
func (l ObjectStoragePlanLimits) ObjectRetentionViolation(r ObjectVersionRetention, now time.Time) *ObjectLockPlanViolation {
	if r.RetainUntilDate != nil && r.RetainUntilDate.After(now) {
		if v := l.lockDays(max(int64(r.RetainUntilDate.Sub(now)/(24*time.Hour)), 1)); v != nil {
			return v
		}
	}
	if r.EventHold == "ON" && r.EventHoldDuration != nil {
		return l.lockDays(r.EventHoldDuration.TotalDays())
	}
	return nil
}

// ObjectLegalHoldViolation refuses placing a legal hold on a plan without
// Object Lock. Releasing one is always allowed.
func (l ObjectStoragePlanLimits) ObjectLegalHoldViolation(h ObjectVersionLegalHold) *ObjectLockPlanViolation {
	if h.Status == "ON" && l.MaxLockRetentionDays <= 0 {
		return &ObjectLockPlanViolation{Kind: "legal_hold", Limit: 0, Observed: 1}
	}
	return nil
}

// ObjectWriteProtectionViolation checks the protection a new version is
// written with.
func (l ObjectStoragePlanLimits) ObjectWriteProtectionViolation(p ObjectWriteProtection, now time.Time) *ObjectLockPlanViolation {
	if p.Retention != nil {
		if v := l.ObjectRetentionViolation(*p.Retention, now); v != nil {
			return v
		}
	}
	if p.LegalHold != nil {
		return l.ObjectLegalHoldViolation(*p.LegalHold)
	}
	return nil
}
