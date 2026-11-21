package whycopy

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestDecorate_AllCodesHaveProse asserts every catalog row has
// non-empty Hint, Why, Fix. A future PR that adds a code to the
// catalog without prose fails here. The tripwire that fails the
// build when a Code… constant in pkg/api/errors.go has no catalog
// row lives in cmd/gregale/lint_tripwires_test.go (out of scope
// for this package, which can't import cmd/gregale).
func TestDecorate_AllCodesHaveProse(t *testing.T) {
	for _, code := range Codes() {
		t.Run(code, func(t *testing.T) {
			r, ok := Lookup(code)
			if !ok {
				t.Fatalf("Lookup(%q) returned ok=false; Codes() included it", code)
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
// row's Hint/Why/Fix onto the supplied Problem.
func TestDecorate_CopiesFields(t *testing.T) {
	p := api.NewProblem(0, api.CodeAppNotListening, "title", "detail")
	got := Decorate(p, api.CodeAppNotListening, nil)
	if got.Hint == "" {
		t.Errorf("Decorate did not set Hint for %s", api.CodeAppNotListening)
	}
	if got.Why == "" {
		t.Errorf("Decorate did not set Why for %s", api.CodeAppNotListening)
	}
	if got.Fix == "" {
		t.Errorf("Decorate did not set Fix for %s", api.CodeAppNotListening)
	}
}

// TestDecorate_ObservedRenderer runs the per-code Observed
// renderer when observed is non-nil, and asserts the templated
// why/fix carry the observed value. Asserts against the field that
// actually carries the templated value per code (the catalog's
// Observed renderer chooses which field to template).
func TestDecorate_ObservedRenderer(t *testing.T) {
	cases := []struct {
		code     string
		observed any
		field    string // "why" or "fix"
		contains string
	}{
		{api.CodeAppNotListening, "8080", "why", ":8080"},
		{api.CodeEnvVarMissing, "DATABASE_URL", "fix", "DATABASE_URL"},
		{api.CodeDepInstallFailed, "npm", "fix", "npm"},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			p := api.NewProblem(0, tc.code, "title", "detail")
			Decorate(p, tc.code, tc.observed)
			var got string
			switch tc.field {
			case "why":
				got = p.Why
			case "fix":
				got = p.Fix
			}
			if !strings.Contains(got, tc.contains) {
				t.Errorf("%s for %s with observed=%v does not contain %q (got %q)",
					tc.field, tc.code, tc.observed, tc.contains, got)
			}
		})
	}
}

// TestDecorate_UnknownCodeNoop asserts Decorate is a no-op for an
// unknown code (no row in catalog). This is the load-bearing
// behaviour for codes that the cluster did not catalog yet — they
// keep their existing wire shape.
func TestDecorate_UnknownCodeNoop(t *testing.T) {
	p := api.NewProblem(0, api.CodePlanLimitRAM, "title", "detail")
	p.Hint = "sentinel"
	got := Decorate(p, "not_a_real_code", nil)
	if got.Hint != "sentinel" {
		t.Errorf("Decorate on unknown code wiped Hint; got %q want sentinel", got.Hint)
	}
	if got.Title != "title" {
		t.Errorf("Decorate on unknown code wiped Title; got %q want title", got.Title)
	}
}

// TestDecorate_CatalogTitleWins asserts Decorate overwrites a
// non-empty Title on the supplied Problem with the catalog row's
// Title — the catalog is the single source of truth for
// customer-facing prose (see whycopy.go::Decorate docstring).
func TestDecorate_CatalogTitleWins(t *testing.T) {
	p := api.NewProblem(0, api.CodeAppNotListening, "constructor-title", "detail")
	got := Decorate(p, api.CodeAppNotListening, nil)
	want := "No process listening on $PORT"
	if got.Title != want {
		t.Errorf("Decorate did not overwrite Title with catalog row: got %q want %q", got.Title, want)
	}
}

// TestDecorate_AppRuntimeOOM_TemplatesPeakAndPlan asserts the
// runtime-OOM Observed renderer templates both the peak MB and
// plan cap MB into the Why + Fix fields. The catalog row is the
// load-bearing UX for the existing-but-previously-uncalled
// CodeAppRuntimeOOM; the templated prose is what the customer
// sees on the deployment-detail page and `gregale inspect --errors`
// after the runtime OOM detection chain (Cluster C) fires.
func TestDecorate_AppRuntimeOOM_TemplatesPeakAndPlan(t *testing.T) {
	p := api.NewProblem(0, api.CodeAppRuntimeOOM, "title", "detail")
	observed := struct{ PeakMB, PlanMB int }{PeakMB: 384, PlanMB: 256}
	Decorate(p, api.CodeAppRuntimeOOM, observed)

	// Why should contain the peak MB and the plan cap + 8 MB
	// overhead phrasing. The cgroup memory.max is set to plan + 8
	// (api.PerVMOverheadMB) on the host side.
	if !strings.Contains(p.Why, "384 MB") {
		t.Errorf("Why missing peak MB; got %q", p.Why)
	}
	if !strings.Contains(p.Why, "256 MB + 8 MB overhead") {
		t.Errorf("Why missing plan cap + 8 MB overhead; got %q", p.Why)
	}

	// Fix names the current limit, the minimum (peak + 8 MB = 392 MB) and a
	// per-app --ram command: RAM is set per app, not only by plan (2026-11-13
	// agent feedback: the old text said to upgrade the plan).
	if !strings.Contains(p.Fix, "from 256 MB") {
		t.Errorf("Fix missing current limit; got %q", p.Fix)
	}
	if !strings.Contains(p.Fix, "at least 392 MB") {
		t.Errorf("Fix missing minimum; got %q", p.Fix)
	}
	if !strings.Contains(p.Fix, "`gregale app <app> --ram 512`") {
		t.Errorf("Fix missing the per-app --ram command; got %q", p.Fix)
	}
}

func TestOOMSuggestedRAMMB(t *testing.T) {
	for _, tc := range []struct{ peak, plan, want int }{
		{peak: 384, plan: 256, want: 512},  // double the limit
		{peak: 520, plan: 512, want: 1024}, // killed at the cap
		{peak: 900, plan: 256, want: 1024}, // peak + 8 beats double, rounded up
		{peak: 0, plan: 512, want: 1024},   // peak unknown
		{peak: 0, plan: 0, want: 0},
	} {
		if got := OOMSuggestedRAMMB(tc.peak, tc.plan); got != tc.want {
			t.Errorf("OOMSuggestedRAMMB(%d, %d) = %d, want %d", tc.peak, tc.plan, got, tc.want)
		}
	}
}

// TestDecorate_AppRuntimeOOM_NilObservedUsesStatic asserts that
// when observed is nil (or the type doesn't match), the static
// Why/Fix is used verbatim. The static prose is the fallback
// for places that stamp CodeAppRuntimeOOM without the runtime
// OOM detection chain (e.g. legacy code paths, future tests).
func TestDecorate_AppRuntimeOOM_NilObservedUsesStatic(t *testing.T) {
	p := api.NewProblem(0, api.CodeAppRuntimeOOM, "title", "detail")
	Decorate(p, api.CodeAppRuntimeOOM, nil)

	// Static Why mentions "+8 MB" but not an observed peak MB.
	if strings.Contains(p.Why, "384 MB") {
		t.Errorf("static Why should not mention peak MB; got %q", p.Why)
	}
	// Static Why still describes the runtime OOM mechanism.
	if !strings.Contains(p.Why, "memory.max") {
		t.Errorf("static Why should describe the cgroup mechanism; got %q", p.Why)
	}
	// Static Fix should NOT carry the templated "at least N MB".
	if !strings.Contains(p.Fix, "`gregale app <app> --ram <MB>`") {
		t.Errorf("static Fix should carry the unobserved prose; got %q", p.Fix)
	}
	if strings.Contains(p.Fix, "at least") {
		t.Errorf("static Fix should not contain templated 'at least N MB'; got %q", p.Fix)
	}
}
