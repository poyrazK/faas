package manifest

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestServingProbesValidation(t *testing.T) {
	ok := func() *ServingProbes {
		return &ServingProbes{URLs: []string{"https://serving-probe.gregale.dev/"}, ExpectedBody: "gregale-serving-probe-ok"}
	}
	with := func(f func(*ServingProbes)) *ServingProbes { p := ok(); f(p); return p }
	for _, tc := range []struct {
		name string
		p    *ServingProbes
		want string
	}{
		{name: "absent"},
		{name: "defaults", p: ok()},
		{name: "full policy", p: with(func(p *ServingProbes) {
			p.Interval, p.TimeoutSeconds, p.MaxFirstByteSeconds, p.MaxAgeSeconds = "2min", 15, 5, 360
		})},
		{name: "no urls", p: with(func(p *ServingProbes) { p.URLs = nil }), want: "serving_probes.urls"},
		{name: "plain http", p: with(func(p *ServingProbes) { p.URLs = []string{"http://serving-probe.gregale.dev/"} }), want: "serving_probes.urls[0]"},
		{name: "fragment", p: with(func(p *ServingProbes) { p.URLs = []string{"https://serving-probe.gregale.dev/#x"} }), want: "serving_probes.urls[0]"},
		{name: "duplicate", p: with(func(p *ServingProbes) { p.URLs = append(p.URLs, p.URLs[0]) }), want: "serving_probes.urls[1]"},
		{name: "no body", p: with(func(p *ServingProbes) { p.ExpectedBody = "" }), want: "serving_probes.expected_body"},
		{name: "newline in body", p: with(func(p *ServingProbes) { p.ExpectedBody = "ok\n" }), want: "serving_probes.expected_body"},
		{name: "delete in body", p: with(func(p *ServingProbes) { p.ExpectedBody = "ok\x7f" }), want: "serving_probes.expected_body"},
		{name: "oversized body", p: with(func(p *ServingProbes) { p.ExpectedBody = strings.Repeat("é", 2049) }), want: "serving_probes.expected_body"},
		{name: "bad interval", p: with(func(p *ServingProbes) { p.Interval = "5m" }), want: "serving_probes.interval"},
		{name: "short timeout", p: with(func(p *ServingProbes) { p.TimeoutSeconds = 0.5 }), want: "serving_probes.timeout_seconds"},
		{name: "infinite timeout", p: with(func(p *ServingProbes) { p.TimeoutSeconds = math.Inf(1) }), want: "serving_probes.timeout_seconds"},
		{name: "budget over timeout", p: with(func(p *ServingProbes) { p.MaxFirstByteSeconds = 21 }), want: "serving_probes.max_first_byte_seconds"},
		{name: "negative budget", p: with(func(p *ServingProbes) { p.MaxFirstByteSeconds = -1 }), want: "serving_probes.max_first_byte_seconds"},
		{name: "max age under three intervals", p: with(func(p *ServingProbes) { p.MaxAgeSeconds = 899 }), want: "serving_probes.max_age_seconds"},
		{name: "max age follows interval", p: with(func(p *ServingProbes) { p.Interval = "10min" }), want: "serving_probes.max_age_seconds"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := tc.p.validate()
			if tc.want == "" {
				if len(errs) != 0 {
					t.Fatalf("errors = %v", errs)
				}
				return
			}
			if len(errs) == 0 || errs[0].Path != tc.want {
				t.Fatalf("errors = %v, want %s", errs, tc.want)
			}
		})
	}
}

func TestServingProbesPolicyLeavesDefaultsToTheRoles(t *testing.T) {
	p := ServingProbes{URLs: []string{"https://serving-probe.gregale.dev/"}, ExpectedBody: "gregale-serving-probe-ok"}
	want := map[string]any{"urls": p.URLs, "expected_body": p.ExpectedBody}
	if got := p.Policy(); !reflect.DeepEqual(got, want) {
		t.Fatalf("policy = %v, want %v", got, want)
	}
	p.Interval, p.TimeoutSeconds, p.MaxFirstByteSeconds, p.MaxAgeSeconds = "2min", 15, 5, 360
	got := p.Policy()
	for key, value := range map[string]any{"interval": "2min", "timeout_seconds": 15.0, "max_first_byte_seconds": 5.0, "max_age_seconds": 360.0} {
		if got[key] != value {
			t.Fatalf("policy[%s] = %v, want %v", key, got[key], value)
		}
	}
}
