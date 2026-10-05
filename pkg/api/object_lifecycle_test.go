package api

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// adr: 550
func TestObjectLifecycleRulesValidation(t *testing.T) {
	ptr := func(v int32) *int32 { return &v }
	marker := true
	date := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	base := ObjectLifecycleRule{ID: "expire", Status: "Enabled", Expiration: &ObjectLifecycleExpiration{Days: ptr(3)}}
	for _, tc := range []struct {
		name  string
		edit  func(*ObjectLifecycleRule)
		valid bool
	}{
		{"days", func(*ObjectLifecycleRule) {}, true},
		{"disabled", func(r *ObjectLifecycleRule) { r.Status = "Disabled" }, true},
		{"empty id", func(r *ObjectLifecycleRule) { r.ID = "" }, true},
		{"unicode id", func(r *ObjectLifecycleRule) { r.ID = strings.Repeat("界", MaxObjectLifecycleRuleIDRunes) }, true},
		{"long id", func(r *ObjectLifecycleRule) { r.ID = strings.Repeat("界", MaxObjectLifecycleRuleIDRunes+1) }, false},
		{"invalid utf8", func(r *ObjectLifecycleRule) { r.ID = string([]byte{0xff}) }, false},
		{"control prefix", func(r *ObjectLifecycleRule) { r.Filter.Prefix = "a\n" }, false},
		{"long prefix", func(r *ObjectLifecycleRule) { r.Filter.Prefix = strings.Repeat("x", MaxObjectS3ListTextBytes+1) }, false},
		{"bad status", func(r *ObjectLifecycleRule) { r.Status = "enabled" }, false},
		{"no action", func(r *ObjectLifecycleRule) { r.Expiration = nil }, false},
		{"zero days", func(r *ObjectLifecycleRule) { r.Expiration.Days = ptr(0) }, false},
		{"negative days", func(r *ObjectLifecycleRule) { r.Expiration.Days = ptr(-1) }, false},
		{"empty expiration", func(r *ObjectLifecycleRule) { r.Expiration = &ObjectLifecycleExpiration{} }, false},
		{"date", func(r *ObjectLifecycleRule) { r.Expiration = &ObjectLifecycleExpiration{Date: &date} }, true},
		{"ambiguous expiration", func(r *ObjectLifecycleRule) { r.Expiration.Date = &date }, false},
		{"non-midnight", func(r *ObjectLifecycleRule) {
			d := date.Add(time.Nanosecond)
			r.Expiration = &ObjectLifecycleExpiration{Date: &d}
		}, false},
		{"tag empty value", func(r *ObjectLifecycleRule) { r.Filter.Tags = map[string]string{"keep": ""} }, true},
		{"tag empty key", func(r *ObjectLifecycleRule) { r.Filter.Tags = map[string]string{"": "keep"} }, false},
		{"tag malformed value", func(r *ObjectLifecycleRule) { r.Filter.Tags = map[string]string{"keep": "\x7f"} }, false},
		{"tag long value", func(r *ObjectLifecycleRule) {
			r.Filter.Tags = map[string]string{"keep": strings.Repeat("x", MaxObjectTagValueBytes+1)}
		}, false},
		{"too many tags", func(r *ObjectLifecycleRule) {
			r.Filter.Tags = map[string]string{}
			for i := range MaxObjectTags + 1 {
				r.Filter.Tags[strings.Repeat("x", i+1)] = ""
			}
		}, false},
		{"tagged abort", func(r *ObjectLifecycleRule) {
			r.Filter.Tags = map[string]string{"x": ""}
			r.AbortIncompleteMultipartDays = ptr(1)
		}, false},
		{"tagged marker", func(r *ObjectLifecycleRule) {
			r.Filter.Tags = map[string]string{"x": ""}
			r.Expiration = &ObjectLifecycleExpiration{ExpiredObjectDeleteMarker: &marker}
		}, false},
		{"retained upper boundary", func(r *ObjectLifecycleRule) {
			r.NoncurrentVersionExpiration = &ObjectLifecycleNoncurrentExpiration{NoncurrentDays: 1, NewerNoncurrentVersions: ptr(MaxObjectLifecycleRetainedVersions)}
		}, true},
		{"retained overflow", func(r *ObjectLifecycleRule) {
			r.NoncurrentVersionExpiration = &ObjectLifecycleNoncurrentExpiration{NoncurrentDays: 1, NewerNoncurrentVersions: ptr(MaxObjectLifecycleRetainedVersions + 1)}
		}, false},
		{"retained zero", func(r *ObjectLifecycleRule) {
			r.NoncurrentVersionExpiration = &ObjectLifecycleNoncurrentExpiration{NoncurrentDays: 1, NewerNoncurrentVersions: ptr(0)}
		}, false},
		{"noncurrent zero", func(r *ObjectLifecycleRule) { r.NoncurrentVersionExpiration = &ObjectLifecycleNoncurrentExpiration{} }, false},
		{"abort zero", func(r *ObjectLifecycleRule) { r.AbortIncompleteMultipartDays = ptr(0) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := CloneObjectLifecycleRules([]ObjectLifecycleRule{base})[0]
			tc.edit(&r)
			_, err := NormalizeObjectLifecycleRules([]ObjectLifecycleRule{r})
			if tc.valid && err != nil || !tc.valid && !errors.Is(err, ErrInvalidObjectLifecycle) {
				t.Fatal(err)
			}
		})
	}
}

// adr: 550
func TestObjectLifecycleRulesNormalizationDetachedAndBounded(t *testing.T) {
	days, keep, abort, marker := int32(3), int32(2), int32(5), true
	date := time.Date(2026, 10, 3, 3, 0, 0, 0, time.FixedZone("local", 3*60*60))
	rules := []ObjectLifecycleRule{
		{Status: "Enabled", Filter: ObjectLifecycleFilter{Tags: map[string]string{"empty": ""}}, Expiration: &ObjectLifecycleExpiration{Days: &days}, NoncurrentVersionExpiration: &ObjectLifecycleNoncurrentExpiration{NoncurrentDays: 1, NewerNoncurrentVersions: &keep}},
		{Status: "Disabled", Expiration: &ObjectLifecycleExpiration{Date: &date}, AbortIncompleteMultipartDays: &abort},
		{Status: "Enabled", Expiration: &ObjectLifecycleExpiration{ExpiredObjectDeleteMarker: &marker}},
	}
	normal, err := NormalizeObjectLifecycleRules(rules)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := NormalizeObjectLifecycleRules(rules)
	if err != nil || !reflect.DeepEqual(normal, replay) || normal[0].ID == normal[1].ID {
		t.Fatal(replay, err)
	}
	if normal[1].Expiration.Date.Location() != time.UTC {
		t.Fatal("date not normalized")
	}
	again, err := NormalizeObjectLifecycleRules(normal)
	if err != nil || !reflect.DeepEqual(normal, again) {
		t.Fatal("normalization not idempotent", err)
	}
	rules[0].Filter.Tags["empty"] = "changed"
	days, keep, abort, marker, date = 99, 99, 99, false, date.Add(time.Hour)
	if normal[0].Filter.Tags["empty"] != "" || *normal[0].Expiration.Days != 3 || *normal[0].NoncurrentVersionExpiration.NewerNoncurrentVersions != 2 || *normal[1].AbortIncompleteMultipartDays != 5 || !*normal[2].Expiration.ExpiredObjectDeleteMarker || normal[1].Expiration.Date.Hour() != 0 {
		t.Fatal("input aliases normalized rules")
	}
	if out, err := NormalizeObjectLifecycleRules(nil); err != nil || out == nil || len(out) != 0 {
		t.Fatal(out, err)
	}
	if _, err := NormalizeObjectLifecycleRules([]ObjectLifecycleRule{normal[0], normal[0]}); !errors.Is(err, ErrInvalidObjectLifecycle) {
		t.Fatal("duplicate IDs", err)
	}
	many := make([]ObjectLifecycleRule, MaxObjectLifecycleRules)
	for i := range many {
		many[i] = CloneObjectLifecycleRules([]ObjectLifecycleRule{normal[0]})[0]
		many[i].ID = ""
	}
	if _, err := NormalizeObjectLifecycleRules(many); err != nil {
		t.Fatal("rule boundary", err)
	}
	if _, err := NormalizeObjectLifecycleRules(append(many, many[0])); !errors.Is(err, ErrInvalidObjectLifecycle) {
		t.Fatal("rule overflow", err)
	}
	// Valid individual fields can still exceed the normalized document budget
	// once JSON escaping is accounted for.
	for i := range many {
		many[i].Filter.Prefix = strings.Repeat("\"", MaxObjectS3ListTextBytes)
		many[i].Filter.Tags = map[string]string{}
		for j := range MaxObjectTags {
			key := strings.Repeat("\"", MaxObjectTagKeyBytes-j)
			many[i].Filter.Tags[key] = strings.Repeat("\"", MaxObjectTagValueBytes)
		}
	}
	if _, err := NormalizeObjectLifecycleRules(many); !errors.Is(err, ErrInvalidObjectLifecycle) {
		t.Fatal("escaped document exceeded budget", err)
	}
}
