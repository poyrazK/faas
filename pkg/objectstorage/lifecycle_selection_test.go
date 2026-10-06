package objectstorage

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func lifecycleInt(v int32) *int32 { return &v }

// adr: 550
func TestLifecycleExpirationSelection(t *testing.T) {
	modified := time.Date(2026, 9, 29, 13, 0, 0, 0, time.UTC)
	deadline := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	marker, noMarker := true, false
	base := LifecycleObject{Key: "logs/a", LastModified: modified, IsLatest: true, OnlyVersion: true, Tags: map[string]string{"ttl": "", "other": "allowed"}}
	rule := api.ObjectLifecycleRule{ID: "ttl", Status: "Enabled", Filter: api.ObjectLifecycleFilter{Prefix: "logs/", Tags: map[string]string{"ttl": ""}}, Expiration: &api.ObjectLifecycleExpiration{Days: lifecycleInt(3)}}
	for _, tc := range []struct {
		name string
		edit func(*LifecycleObject, *api.ObjectLifecycleRule)
		now  time.Time
		kind string
	}{
		{"before rounded day", func(*LifecycleObject, *api.ObjectLifecycleRule) {}, deadline.Add(-time.Nanosecond), ""},
		{"at rounded day", func(*LifecycleObject, *api.ObjectLifecycleRule) {}, deadline, "current"},
		{"missing empty-valued tag", func(o *LifecycleObject, _ *api.ObjectLifecycleRule) { o.Tags = nil }, deadline, ""},
		{"wrong tag", func(o *LifecycleObject, _ *api.ObjectLifecycleRule) { o.Tags = map[string]string{"ttl": "different"} }, deadline, ""},
		{"wrong prefix", func(o *LifecycleObject, _ *api.ObjectLifecycleRule) { o.Key = "other/a" }, deadline, ""},
		{"disabled", func(_ *LifecycleObject, r *api.ObjectLifecycleRule) { r.Status = "Disabled" }, deadline, ""},
		{"date before", func(_ *LifecycleObject, r *api.ObjectLifecycleRule) {
			r.Expiration = &api.ObjectLifecycleExpiration{Date: &deadline}
		}, deadline.Add(-time.Second), ""},
		{"date at", func(_ *LifecycleObject, r *api.ObjectLifecycleRule) {
			r.Expiration = &api.ObjectLifecycleExpiration{Date: &deadline}
		}, deadline, "current"},
		{"sole aged marker", func(o *LifecycleObject, r *api.ObjectLifecycleRule) { o.DeleteMarker = true; r.Filter.Tags = nil }, deadline, "expired_marker"},
		{"marker with history", func(o *LifecycleObject, r *api.ObjectLifecycleRule) {
			o.DeleteMarker = true
			o.OnlyVersion = false
			r.Filter.Tags = nil
		}, deadline, ""},
		{"explicit marker", func(o *LifecycleObject, r *api.ObjectLifecycleRule) {
			o.DeleteMarker = true
			r.Filter.Tags = nil
			r.Expiration = &api.ObjectLifecycleExpiration{ExpiredObjectDeleteMarker: &marker}
		}, modified, "expired_marker"},
		{"false marker", func(o *LifecycleObject, r *api.ObjectLifecycleRule) {
			o.DeleteMarker = true
			r.Filter.Tags = nil
			r.Expiration = &api.ObjectLifecycleExpiration{ExpiredObjectDeleteMarker: &noMarker}
		}, deadline, ""},
		{"marker-only rule leaves data", func(_ *LifecycleObject, r *api.ObjectLifecycleRule) {
			r.Filter.Tags = nil
			r.Expiration = &api.ObjectLifecycleExpiration{ExpiredObjectDeleteMarker: &marker}
		}, deadline, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := base
			r := api.CloneObjectLifecycleRules([]api.ObjectLifecycleRule{rule})[0]
			tc.edit(&o, &r)
			d, err := SelectLifecycleAction([]api.ObjectLifecycleRule{r}, o, tc.now)
			if err != nil || d.Kind != tc.kind || d.Kind != "" && d.RuleID != r.ID {
				t.Fatal(d, err)
			}
		})
	}
}

// adr: 550
func TestLifecycleNoncurrentAgeAndRetention(t *testing.T) {
	now := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	r := api.ObjectLifecycleRule{ID: "history", Status: "Enabled", NoncurrentVersionExpiration: &api.ObjectLifecycleNoncurrentExpiration{NoncurrentDays: 3, NewerNoncurrentVersions: lifecycleInt(2)}}
	o := LifecycleObject{Key: "a", LastModified: now.AddDate(0, -1, 0), NoncurrentSince: now.AddDate(0, 0, -4).Add(time.Hour), NewerNoncurrent: 2}
	for _, tc := range []struct {
		name  string
		since time.Time
		newer int
		kind  string
	}{
		{"two newer retained", o.NoncurrentSince, 2, "noncurrent"},
		{"too few newer", o.NoncurrentSince, 1, ""},
		{"recent successor despite old object", now.Add(-time.Hour), 2, ""},
		{"before rounded successor deadline", now.AddDate(0, 0, -3).Add(time.Hour), 2, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := o
			v.NoncurrentSince, v.NewerNoncurrent = tc.since, tc.newer
			d, err := SelectLifecycleAction([]api.ObjectLifecycleRule{r}, v, now)
			if err != nil || d.Kind != tc.kind {
				t.Fatal(d, err)
			}
		})
	}
	for _, edit := range []func(*LifecycleObject){
		func(v *LifecycleObject) { v.NoncurrentSince = time.Time{} },
		func(v *LifecycleObject) { v.NoncurrentSince = v.LastModified.Add(-time.Second) },
		func(v *LifecycleObject) { v.OnlyVersion = true },
		func(v *LifecycleObject) { v.NewerNoncurrent = -1 },
		func(v *LifecycleObject) { v.IsLatest = true },
		func(v *LifecycleObject) { v.LastModified = now.Add(time.Hour) },
	} {
		v := o
		edit(&v)
		if _, err := SelectLifecycleAction([]api.ObjectLifecycleRule{r}, v, now); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid history", v, err)
		}
	}
}

// adr: 550
func TestLifecycleDayDeadlineAndMultipart(t *testing.T) {
	for _, tc := range []struct {
		base string
		days int32
		want string
	}{
		{"2014-01-15T10:30:00Z", 3, "2014-01-19T00:00:00Z"},
		{"2026-10-01T00:00:00Z", 1, "2026-10-03T00:00:00Z"},
		{"2026-10-01T03:00:00+03:00", 1, "2026-10-03T00:00:00Z"},
		{"2024-02-28T23:59:59Z", 1, "2024-03-01T00:00:00Z"},
	} {
		base, err := time.Parse(time.RFC3339, tc.base)
		if err != nil {
			t.Fatal(err)
		}
		if got := lifecycleDayDeadline(base, tc.days).Format(time.RFC3339); got != tc.want {
			t.Fatal(got, tc.want)
		}
	}
	now := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	r := api.ObjectLifecycleRule{ID: "abort", Status: "Enabled", Filter: api.ObjectLifecycleFilter{Prefix: "tmp/"}, AbortIncompleteMultipartDays: lifecycleInt(2)}
	initiated := now.AddDate(0, 0, -3).Add(time.Hour)
	for _, tc := range []struct {
		key  string
		when time.Time
		kind string
	}{
		{"tmp/a", initiated, "abort_multipart"}, {"other/a", initiated, ""}, {"tmp/a", now.Add(-time.Hour), ""},
	} {
		d, err := LifecycleMultipartAbortDue([]api.ObjectLifecycleRule{r}, tc.key, tc.when, now)
		if err != nil || d.Kind != tc.kind {
			t.Fatal(d, err)
		}
	}
	r.Filter.Tags = map[string]string{"ttl": ""}
	if _, err := LifecycleMultipartAbortDue([]api.ObjectLifecycleRule{r}, "tmp/a", initiated, now); !errors.Is(err, ErrInvalid) {
		t.Fatal("tag-filtered abort accepted", err)
	}
}
