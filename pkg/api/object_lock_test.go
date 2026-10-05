package api

import (
	"testing"
	"time"
)

// adr: 563
func TestObjectLockPolicyValidation(t *testing.T) {
	day, year, zero, negative, tooMany := int32(1), int32(100), int32(0), int32(-1), MaxObjectLockRetentionDays+1
	for _, tc := range []struct {
		name  string
		p     ObjectRetentionPeriod
		valid bool
	}{
		{"days", ObjectRetentionPeriod{Days: &day}, true},
		{"years_limit", ObjectRetentionPeriod{Years: &year}, true},
		{"absent", ObjectRetentionPeriod{}, false},
		{"both", ObjectRetentionPeriod{Days: &day, Years: &year}, false},
		{"zero", ObjectRetentionPeriod{Days: &zero}, false},
		{"negative", ObjectRetentionPeriod{Years: &negative}, false},
		{"days_limit", ObjectRetentionPeriod{Days: &tooMany}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.p.Valid() != tc.valid {
				t.Fatal(tc.p, tc.valid)
			}
		})
	}
	for _, tc := range []struct {
		name  string
		c     ObjectBucketObjectLockConfiguration
		valid bool
	}{
		{"disabled_read", ObjectBucketObjectLockConfiguration{}, true},
		{"enabled_without_default", ObjectBucketObjectLockConfiguration{Enabled: true}, true},
		{"fixed", ObjectBucketObjectLockConfiguration{Enabled: true, DefaultRetention: &ObjectLockDefaultRetention{Mode: "GOVERNANCE", Days: &day}}, true},
		{"event", ObjectBucketObjectLockConfiguration{Enabled: true, DefaultRetention: &ObjectLockDefaultRetention{Mode: "COMPLIANCE", DefaultEventHold: &ObjectRetentionPeriod{Years: &year}}}, true},
		{"both_periods", ObjectBucketObjectLockConfiguration{Enabled: true, DefaultRetention: &ObjectLockDefaultRetention{Mode: "COMPLIANCE", Days: &day, DefaultEventHold: &ObjectRetentionPeriod{Years: &year}}}, true},
		{"disabled_with_default", ObjectBucketObjectLockConfiguration{DefaultRetention: &ObjectLockDefaultRetention{Mode: "GOVERNANCE", Days: &day}}, false},
		{"empty_default", ObjectBucketObjectLockConfiguration{Enabled: true, DefaultRetention: &ObjectLockDefaultRetention{}}, false},
		{"unknown_mode", ObjectBucketObjectLockConfiguration{Enabled: true, DefaultRetention: &ObjectLockDefaultRetention{Mode: "governance", Days: &day}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.c.Valid() != tc.valid {
				t.Fatal(tc.c, tc.valid)
			}
		})
	}
	date := time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC)
	invalidDate := time.Time{}
	for _, tc := range []struct {
		name  string
		r     ObjectVersionRetention
		valid bool
	}{
		{"clear", ObjectVersionRetention{}, true},
		{"fixed", ObjectVersionRetention{Mode: "GOVERNANCE", RetainUntilDate: &date}, true},
		{"event_on", ObjectVersionRetention{Mode: "COMPLIANCE", EventHold: "ON", EventHoldDuration: &ObjectRetentionPeriod{Days: &day}}, true},
		{"event_release", ObjectVersionRetention{Mode: "COMPLIANCE", EventHold: "OFF"}, true},
		{"minimum_date", ObjectVersionRetention{Mode: "COMPLIANCE", RetainUntilDate: &date, EventHold: "ON", EventHoldDuration: &ObjectRetentionPeriod{Days: &day}}, true},
		{"mode_only", ObjectVersionRetention{Mode: "COMPLIANCE"}, false},
		{"date_only", ObjectVersionRetention{RetainUntilDate: &date}, false},
		{"zero_date", ObjectVersionRetention{Mode: "GOVERNANCE", RetainUntilDate: &invalidDate}, false},
		{"missing_duration", ObjectVersionRetention{Mode: "COMPLIANCE", EventHold: "ON"}, false},
		{"duration_without_hold", ObjectVersionRetention{Mode: "COMPLIANCE", RetainUntilDate: &date, EventHoldDuration: &ObjectRetentionPeriod{Days: &day}}, false},
		{"unknown_hold", ObjectVersionRetention{Mode: "COMPLIANCE", EventHold: "on", EventHoldDuration: &ObjectRetentionPeriod{Days: &day}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.r.Valid() != tc.valid {
				t.Fatal(tc.r, tc.valid)
			}
		})
	}
	for _, status := range []string{"", "ON", "OFF", "on", "UNKNOWN"} {
		if (ObjectVersionLegalHold{Status: status}).Valid() != (status == "ON" || status == "OFF") {
			t.Fatal(status)
		}
	}
}

// adr: 563
func TestObjectLockPoliciesClonePrivatePointers(t *testing.T) {
	day, year := int32(1), int32(2)
	c := ObjectBucketObjectLockConfiguration{Enabled: true, DefaultRetention: &ObjectLockDefaultRetention{Mode: "COMPLIANCE", Days: &day, DefaultEventHold: &ObjectRetentionPeriod{Years: &year}}}
	copy := c.Clone()
	date := time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC)
	r := ObjectVersionRetention{Mode: "COMPLIANCE", RetainUntilDate: &date, EventHold: "ON", EventHoldDuration: &ObjectRetentionPeriod{Days: &day}}
	retention := r.Clone()
	day, year = 4, 5
	date = date.Add(time.Hour)
	c.DefaultRetention.Mode = "GOVERNANCE"
	if *copy.DefaultRetention.Days != 1 || *copy.DefaultRetention.DefaultEventHold.Years != 2 || copy.DefaultRetention.Mode != "COMPLIANCE" || *retention.EventHoldDuration.Days != 1 || retention.RetainUntilDate.Equal(date) {
		t.Fatal("policy pointers escaped snapshot")
	}
	if (ObjectBucketObjectLockConfiguration{}).Clone().DefaultRetention != nil || (ObjectVersionRetention{}).Clone().RetainUntilDate != nil {
		t.Fatal("empty snapshot changed")
	}
}

// adr: 563
func TestObjectLockWriteDateNeverShortens(t *testing.T) {
	for _, nanos := range []int{0, 1, 1000000, 123456789, 999999999} {
		d := time.Date(2028, 1, 1, 0, 0, 0, nanos, time.FixedZone("offset", 3600))
		r := ObjectVersionRetention{Mode: "COMPLIANCE", RetainUntilDate: &d}
		n := r.ForWrite()
		if !n.Valid() || n.RetainUntilDate.Before(d) || n.RetainUntilDate.Sub(d) >= time.Millisecond || n.RetainUntilDate.Location() != time.UTC || n.RetainUntilDate.Nanosecond()%int(time.Millisecond) != 0 || r.RetainUntilDate.Nanosecond() != nanos {
			t.Fatal("serialization changed intent", r, n)
		}
	}
	d := time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC)
	if (ObjectVersionRetention{Mode: "COMPLIANCE", RetainUntilDate: &d}).ForWrite().Valid() {
		t.Fatal("rounding overflow accepted")
	}
}

// adr: 563
func TestObjectLockReleaseCannotChangeDuration(t *testing.T) {
	day := int32(1)
	date := time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC)
	r := ObjectVersionRetention{Mode: "COMPLIANCE", EventHold: "OFF", EventHoldDuration: &ObjectRetentionPeriod{Days: &day}, RetainUntilDate: &date}
	if !r.Valid() || r.ValidForWrite() {
		t.Fatal("release observation became a valid duration-changing intent", r)
	}
	r.EventHoldDuration = nil
	if !r.ValidForWrite() {
		t.Fatal("fixed release intent rejected", r)
	}
	r.RetainUntilDate = nil
	if !r.ValidForWrite() {
		t.Fatal("release transition must use stored duration", r)
	}
}
