package routeadvisor

// adr: 1188

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestContentWindowsMatchTheStoredOutcomes(t *testing.T) {
	// The advice query and the request_telemetry CHECK name these windows.
	want := []time.Duration{time.Minute, 5 * time.Minute, time.Hour}
	if len(api.RouteContentWindows) != len(want) {
		t.Fatalf("windows = %v", api.RouteContentWindows)
	}
	for i := range want {
		if api.RouteContentWindows[i] != want[i] {
			t.Fatalf("windows = %v, want %v", api.RouteContentWindows, want)
		}
	}
}

func TestContentVerdict(t *testing.T) {
	tests := []struct {
		name       string
		stats      ContentStats
		wantSafe   time.Duration
		wantUnsafe string
	}{
		{"no evidence", ContentStats{}, 0, ""},
		{"too few repeats", ContentStats{Same: []int64{20, 10, 0}, Changed: []int64{0, 0, 0}}, 0, ""},
		{"stable for an hour", ContentStats{First: 30, Same: []int64{300, 200, 100}, Changed: []int64{0, 1, 2}}, time.Hour, ""},
		{"changes every few minutes", ContentStats{Same: []int64{200, 20, 0}, Changed: []int64{1, 80, 90}}, time.Minute, ""},
		{"changes between close repeats", ContentStats{Same: []int64{80, 0, 0}, Changed: []int64{20, 0, 0}}, 0, "less than a minute"},
		{"mostly uncacheable", ContentStats{Uncacheable: 400, First: 10, Same: []int64{60, 0, 0}, Changed: []int64{0, 0, 0}}, time.Minute, "cannot be stored"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := tt.stats.Verdict()
			if v.SafeMaxAge != tt.wantSafe {
				t.Fatalf("safe max-age = %v, want %v (%+v)", v.SafeMaxAge, tt.wantSafe, v)
			}
			if (tt.wantUnsafe == "") != (v.Unsafe == "") || !strings.Contains(v.Unsafe, tt.wantUnsafe) {
				t.Fatalf("unsafe = %q, want %q", v.Unsafe, tt.wantUnsafe)
			}
		})
	}
}

func TestCacheSuggestionUsesContentEvidence(t *testing.T) {
	stable := ContentStats{First: 30, Same: []int64{300, 200, 100}, Changed: []int64{0, 1, 2}}
	r := cacheRoute()
	r.Content = stable
	got := Advise(baseInputs(r))
	if len(got) != 1 {
		t.Fatalf("suggestions = %+v", got)
	}
	s := got[0]
	if s.Evidence.SafeMaxAgeSeconds != 3600 || s.Evidence.ContentRepeats != 600 || s.Evidence.ContentChanges != 3 || s.Evidence.ContentObserved != 633 {
		t.Fatalf("evidence = %+v", s.Evidence)
	}
	if !strings.Contains(s.Rationale, "Measured: 600 repeat requests within 1h0m0s") || !strings.Contains(s.Cautions[0], "--cache-max-age 3600") {
		t.Fatalf("rationale %q cautions %q", s.Rationale, s.Cautions)
	}

	// A requested max-age longer than the measured one is flagged.
	r.Content = ContentStats{Same: []int64{200, 20, 0}, Changed: []int64{1, 80, 90}}
	in := baseInputs(r)
	in.CacheMaxAgeSeconds = 300
	if got := Advise(in); len(got) != 1 || !strings.Contains(got[0].Cautions[0], "longer than the 1m0s the gateways measured as safe") {
		t.Fatalf("cautions = %+v", got)
	}

	// Unsafe evidence withholds the suggestion.
	r.Content = ContentStats{Same: []int64{80, 0, 0}, Changed: []int64{20, 0, 0}}
	if got := Advise(baseInputs(r)); len(got) != 0 {
		t.Fatalf("an often-changing response must not be suggested for caching: %+v", got)
	}

	// Without evidence the suggestion stands, with the unmeasured caution.
	r.Content = ContentStats{}
	if got := Advise(baseInputs(r)); len(got) != 1 || !strings.Contains(got[0].Cautions[0], "not yet seen enough repeat requests") || got[0].Evidence.SafeMaxAgeSeconds != 0 {
		t.Fatalf("unmeasured = %+v", got)
	}
}
