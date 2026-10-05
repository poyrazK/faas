package api

import "time"

// ObjectRetentionPeriod specifies exactly one positive duration. The bounds
// also apply to native event holds and keep owned policies portable.
type ObjectRetentionPeriod struct {
	Days  *int32 `json:"days,omitempty"`
	Years *int32 `json:"years,omitempty"`
}

func (p ObjectRetentionPeriod) Valid() bool {
	return p.Days != nil && p.Years == nil && *p.Days > 0 && *p.Days <= MaxObjectLockRetentionDays ||
		p.Years != nil && p.Days == nil && *p.Years > 0 && *p.Years <= MaxObjectLockRetentionYears
}

func (p ObjectRetentionPeriod) Clone() ObjectRetentionPeriod {
	if p.Days != nil {
		d := *p.Days
		p.Days = &d
	}
	if p.Years != nil {
		y := *p.Years
		p.Years = &y
	}
	return p
}

// Clearing DefaultRetention preserves Enabled. Object Lock cannot be disabled.
type ObjectBucketObjectLockConfiguration struct {
	Enabled          bool                        `json:"enabled"`
	DefaultRetention *ObjectLockDefaultRetention `json:"default_retention,omitempty"`
}

type ObjectLockDefaultRetention struct {
	Mode             string                 `json:"mode"`
	Days             *int32                 `json:"days,omitempty"`
	Years            *int32                 `json:"years,omitempty"`
	DefaultEventHold *ObjectRetentionPeriod `json:"default_event_hold,omitempty"`
}

func ValidObjectLockMode(mode string) bool { return mode == "GOVERNANCE" || mode == "COMPLIANCE" }

func (r ObjectLockDefaultRetention) Valid() bool {
	if !ValidObjectLockMode(r.Mode) || r.DefaultEventHold != nil && !r.DefaultEventHold.Valid() {
		return false
	}
	if r.Days == nil && r.Years == nil {
		return r.DefaultEventHold != nil
	}
	return (ObjectRetentionPeriod{Days: r.Days, Years: r.Years}).Valid()
}

func (c ObjectBucketObjectLockConfiguration) Valid() bool {
	return (c.Enabled || c.DefaultRetention == nil) && (c.DefaultRetention == nil || c.DefaultRetention.Valid())
}

func (c ObjectBucketObjectLockConfiguration) Clone() ObjectBucketObjectLockConfiguration {
	if c.DefaultRetention == nil {
		return c
	}
	r := *c.DefaultRetention
	p := (ObjectRetentionPeriod{Days: r.Days, Years: r.Years}).Clone()
	r.Days, r.Years = p.Days, p.Years
	if r.DefaultEventHold != nil {
		h := r.DefaultEventHold.Clone()
		r.DefaultEventHold = &h
	}
	c.DefaultRetention = &r
	return c
}

// An empty retention is an explicit clear request. EventHold OFF releases
// an existing hold without specifying a new duration; the provider computes
// the final retain-until date. Legal holds are managed independently.
type ObjectVersionRetention struct {
	Mode              string                 `json:"mode,omitempty"`
	RetainUntilDate   *time.Time             `json:"retain_until_date,omitempty"`
	EventHold         string                 `json:"event_hold,omitempty"`
	EventHoldDuration *ObjectRetentionPeriod `json:"event_hold_duration,omitempty"`
}

func (r ObjectVersionRetention) Empty() bool {
	return r.Mode == "" && r.RetainUntilDate == nil && r.EventHold == "" && r.EventHoldDuration == nil
}

func (r ObjectVersionRetention) Valid() bool {
	if r.Empty() {
		return true
	}
	if !ValidObjectLockMode(r.Mode) || r.EventHold != "" && r.EventHold != "ON" && r.EventHold != "OFF" {
		return false
	}
	if r.RetainUntilDate != nil && (r.RetainUntilDate.IsZero() || r.RetainUntilDate.Year() < 1 || r.RetainUntilDate.Year() > 9999) {
		return false
	}
	if r.EventHoldDuration != nil && (r.EventHold == "" || !r.EventHoldDuration.Valid()) {
		return false
	}
	if r.EventHold == "ON" && r.EventHoldDuration == nil {
		return false
	}
	return r.RetainUntilDate != nil || r.EventHold != ""
}

// Native observations may retain the existing duration after release. New OFF
// requests must omit that duration; the provider fixes the date from its stored
// duration. An OFF intent without a date is conditional on a previous ON hold.
func (r ObjectVersionRetention) ValidForWrite() bool {
	return r.Valid() && (r.EventHold != "OFF" || r.EventHoldDuration == nil)
}

func (r ObjectVersionRetention) Clone() ObjectVersionRetention {
	if r.RetainUntilDate != nil {
		d := *r.RetainUntilDate
		r.RetainUntilDate = &d
	}
	if r.EventHoldDuration != nil {
		d := r.EventHoldDuration.Clone()
		r.EventHoldDuration = &d
	}
	return r
}

// ForWrite snapshots a new intent at the native SDK's millisecond precision.
// Round upwards so serialization cannot shorten a customer's requested lock.
// Validate the result before admission; rounding may exceed year 9999.
// Provider observations retain their original precision instead.
func (r ObjectVersionRetention) ForWrite() ObjectVersionRetention {
	r = r.Clone()
	if r.RetainUntilDate != nil {
		d := r.RetainUntilDate.UTC()
		t := d.Truncate(time.Millisecond)
		if t.Before(d) {
			t = t.Add(time.Millisecond)
		}
		r.RetainUntilDate = &t
	}
	return r
}

type ObjectVersionLegalHold struct {
	Status string `json:"status"`
}

func (h ObjectVersionLegalHold) Valid() bool { return h.Status == "ON" || h.Status == "OFF" }
