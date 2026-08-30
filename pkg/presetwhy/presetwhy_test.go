package presetwhy

import (
	"strings"
	"testing"
)

// TestDecorate_AllCodesHaveProse asserts every catalog row has
// non-empty Title, Hint, Why, Fix. A future PR that adds a preset
// name to the catalog without prose fails here. The tripwire that
// fails the build when a preset name in
// migrations/00418_alert_presets_seed.sql has no catalog row lives
// in cmd/gregale/lint_tripwires_test.go (out of scope for this
// package, which can't import cmd/gregale).
func TestDecorate_AllCodesHaveProse(t *testing.T) {
	for _, code := range Codes() {
		t.Run(code, func(t *testing.T) {
			r, ok := Lookup(code)
			if !ok {
				t.Fatalf("Lookup(%q) returned ok=false; Codes() included it", code)
			}
			if r.Title == "" {
				t.Errorf("code=%s: Title is empty; customer sees no bold summary", code)
			}
			if r.Hint == "" {
				t.Errorf("code=%s: Hint is empty; customer sees no next-action line", code)
			}
			if r.Why == "" {
				t.Errorf("code=%s: Why is empty; customer sees no cause explanation", code)
			}
			if r.Fix == "" {
				t.Errorf("code=%s: Fix is empty; customer sees no remediation", code)
			}
			if len(r.Hint) > 200 {
				t.Errorf("code=%s: Hint is %d bytes; one-line renderer expects ≤200", code, len(r.Hint))
			}
			if len(r.Fix) > 512 {
				t.Errorf("code=%s: Fix is %d bytes; CLI tripwire caps at 512", code, len(r.Fix))
			}
		})
	}
}

// TestDecorate_CopiesFields asserts Decorate copies the catalog
// row's Title/Hint/Why/Fix into a fresh *Explanation.
func TestDecorate_CopiesFields(t *testing.T) {
	got := Decorate("api_down", 0)
	if got == nil {
		t.Fatalf("Decorate(\"api_down\", 0) returned nil; want populated *Explanation")
	}
	if got.Title == "" {
		t.Errorf("Decorate did not set Title for api_down")
	}
	if got.Hint == "" {
		t.Errorf("Decorate did not set Hint for api_down")
	}
	if got.Why == "" {
		t.Errorf("Decorate did not set Why for api_down")
	}
	if got.Fix == "" {
		t.Errorf("Decorate did not set Fix for api_down")
	}
}

// TestDecorate_ObservedRenderer runs the per-preset Observed renderer
// when observed is non-zero, and asserts the templated why/fix carry
// the observed value. Asserts against the field that actually carries
// the templated value per preset.
func TestDecorate_ObservedRenderer(t *testing.T) {
	cases := []struct {
		preset   string
		observed float64
		field    string // "why" or "fix"
		contains string
	}{
		// spend_eur_20 observed=22.5 → Why carries "€22.50"
		{"spend_eur_20", 22.5, "why", "€22.50"},
		// error_rate_2pct observed=4.2 → Fix carries "4.20%" templating
		{"error_rate_2pct", 4.2, "fix", "4.20"},
		// cert_expiring_14d observed=864000 (10 days) → Why carries
		// "10.0 days" — the Observed renderer converts seconds→days.
		{"cert_expiring_14d", 864000, "why", "days"},
		// queue_backlog_growing observed=75 → Why carries "75"
		{"queue_backlog_growing", 75, "why", "75"},
	}
	for _, tc := range cases {
		t.Run(tc.preset, func(t *testing.T) {
			got := Decorate(tc.preset, tc.observed)
			if got == nil {
				t.Fatalf("Decorate(%q, %v) returned nil", tc.preset, tc.observed)
			}
			var s string
			switch tc.field {
			case "why":
				s = got.Why
			case "fix":
				s = got.Fix
			}
			if !strings.Contains(s, tc.contains) {
				t.Errorf("%s for %s with observed=%v does not contain %q (got %q)",
					tc.field, tc.preset, tc.observed, tc.contains, s)
			}
		})
	}
}

// TestDecorate_UnknownPresetReturnsNil asserts Decorate returns nil
// for an unknown preset name. This is the load-bearing behaviour for
// catalog rows that haven't been documented yet — the dashboard
// template uses `with` to skip the panel cleanly when the function
// returns nil.
func TestDecorate_UnknownPresetReturnsNil(t *testing.T) {
	if got := Decorate("not_a_real_preset", 0); got != nil {
		t.Errorf("Decorate on unknown preset returned non-nil; got %+v want nil", got)
	}
}

// TestDecorate_ZeroObservedUsesStatic asserts that when observed is
// zero (the dashboard renders this row at instantiation time, before
// the alert has fired), the static Why/Fix is used verbatim. The
// static prose is the fallback for every card on the grid — the
// Observed renderer only takes over when an alert fires and the
// observed value lands in the dashboard's alert-detail panel.
func TestDecorate_ZeroObservedUsesStatic(t *testing.T) {
	got := Decorate("spend_eur_20", 0)
	if got == nil {
		t.Fatalf("Decorate returned nil; want populated *Explanation")
	}
	// Static Why mentions "€20 threshold" but no observed value.
	if !strings.Contains(got.Why, "€20") {
		t.Errorf("static Why missing '€20' threshold phrasing; got %q", got.Why)
	}
	if strings.Contains(got.Why, "€22.50") {
		t.Errorf("static Why should not contain templated '€22.50'; got %q", got.Why)
	}
	// Static Fix carries the unobserved prose.
	if !strings.Contains(got.Fix, "Scale plan") {
		t.Errorf("static Fix should carry the unobserved Scale-plan hint; got %q", got.Fix)
	}
	if strings.Contains(got.Fix, "€22.50 is unusual") {
		t.Errorf("static Fix should not contain templated observed prose; got %q", got.Fix)
	}
}

// TestCodes_SortedAndComplete asserts Codes returns a deterministic
// sorted list of all 8 catalog rows (the 3 originally-enabled + the
// 5 newly-enabled signals). The tripwire in
// cmd/gregale/lint_tripwires_test.go uses Codes() for the inverse
// membership check (every preset seed row → catalog row), so a
// stable ordering keeps the diff readable when a row is added.
func TestCodes_SortedAndComplete(t *testing.T) {
	codes := Codes()
	if len(codes) != 8 {
		t.Errorf("Codes() returned %d entries; want 8 (3 originally-enabled + 5 newly-enabled signals)", len(codes))
	}
	// Spot-check the first + last (sorted) entries.
	if codes[0] != "api_down" {
		t.Errorf("Codes()[0] = %q; want \"api_down\" (alphabetically first)", codes[0])
	}
	if codes[len(codes)-1] != "spend_eur_20" {
		t.Errorf("Codes()[last] = %q; want \"spend_eur_20\" (alphabetically last among non-numerics)", codes[len(codes)-1])
	}
	// Verify Codes is sorted.
	for i := 1; i < len(codes); i++ {
		if codes[i-1] >= codes[i] {
			t.Errorf("Codes() not sorted at index %d: %q >= %q", i, codes[i-1], codes[i])
		}
	}
}
