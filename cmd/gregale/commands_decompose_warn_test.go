package main

// commands_decompose_warn_test.go — unit tests for the ADR-124
// ship-blocker #4 PrintWarn hook in printPlanText. The warn fires
// when --exclude produced a destructive subset (plan.Removed
// non-empty) AND the operator did not ask for the full partition
// view (showAffected=false). All other combinations must stay
// silent so the default render stays terse.
//
// Pins:
//
//  1. (exclude=non-empty, Removed=non-empty, showAffected=false)
//     → PrintWarn fires (the destructive-nudge case)
//  2. (exclude=non-empty, Removed=non-empty, showAffected=true)
//     → no warn (operator already sees the partition)
//  3. (exclude=empty,    Removed=non-empty, showAffected=false)
//     → no warn (no --exclude → no destructive intent)
//  4. (exclude=non-empty, Removed=empty,    showAffected=false)
//     → no warn (--exclude did not produce soft-deletes)
//  5. (exclude=non-empty, Removed=non-empty, showAffected=false)
//     → ! WARNING glyph prefix (writeStatus contract)

import (
	"bytes"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// basePlan returns a minimal PlanResponse with the fields
// printPlanText reads. can_apply=true keeps the test focused on
// the warn path (the can_apply=false branch returns early).
func basePlan() api.PlanResponse {
	return api.PlanResponse{
		ProjectSlug:   "demo",
		ScanSource:    "compose",
		Tier:          "single",
		ObservedApps:  1,
		LimitApps:     5,
		ObservedCrons: 0,
		LimitCrons:    10,
		CanApply:      true,
		Removed:       []string{"checkout-api"},
	}
}

// TestPrintPlanText_WarnsOnDestructiveExclude pins the load-
// bearing case: --exclude is set, plan.Removed is non-empty,
// showAffected=false → PrintWarn fires. Asserts the warning
// prefix glyph (the writeStatus "!" convention) AND the
// message body. A refactor that drops either would leave the
// operator without the destructive-subset signal.
func TestPrintPlanText_WarnsOnDestructiveExclude(t *testing.T) {
	// Pin the TTY seam so the glyph assertion is deterministic
	// regardless of sibling-test state (jsonOutput / noColorVal
	// can leak across the gregale package's many tests).
	prevTTY := testOnlyTTY
	prevJSON := jsonOutput
	on := true
	testOnlyTTY = &on
	jsonOutput = false
	t.Cleanup(func() {
		testOnlyTTY = prevTTY
		jsonOutput = prevJSON
	})

	var buf bytes.Buffer
	exit := printPlanText(&buf, basePlan(), []string{"checkout-web"}, false)
	if exit != 0 {
		t.Fatalf("printPlanText exit code: got %d, want 0", exit)
	}
	out := buf.String()
	if !strings.Contains(out, "Applying will soft-delete") {
		t.Fatalf("missing warning body; output:\n%s", out)
	}
	if !strings.Contains(out, "1 app(s)") {
		t.Fatalf("missing count (1) in the warning; output:\n%s", out)
	}
	if !strings.Contains(out, "!") {
		t.Fatalf("missing warning glyph '!'; writeStatus contract broken; output:\n%s", out)
	}
}

// TestPrintPlanText_NoWarnOnShowAffected pins the negative case
// where the operator already opted into the partition view: a
// --show-affected render must NOT add a redundant warning. The
// warn exists to nudge operators who didn't see the partition;
// surfacing the partition + warning would double-up.
func TestPrintPlanText_NoWarnOnShowAffected(t *testing.T) {
	var buf bytes.Buffer
	exit := printPlanText(&buf, basePlan(), []string{"checkout-web"}, true)
	if exit != 0 {
		t.Fatalf("printPlanText exit code: got %d, want 0", exit)
	}
	out := buf.String()
	if strings.Contains(out, "Applying will soft-delete") {
		t.Fatalf("unexpected warning with --show-affected; output:\n%s", out)
	}
}

// TestPrintPlanText_NoWarnWithoutExclude pins the case where
// --exclude is empty: no operator exclude-intent → no warn.
// A non-empty plan.Removed from a scan that simply dropped
// existing apps is not a destructive-exclude scenario; the
// regular partition view surfaces it without a warning.
func TestPrintPlanText_NoWarnWithoutExclude(t *testing.T) {
	plan := basePlan()
	plan.Removed = []string{"checkout-api"}

	var buf bytes.Buffer
	exit := printPlanText(&buf, plan, nil, false)
	if exit != 0 {
		t.Fatalf("printPlanText exit code: got %d, want 0", exit)
	}
	out := buf.String()
	if strings.Contains(out, "Applying will soft-delete") {
		t.Fatalf("unexpected warning without --exclude; output:\n%s", out)
	}
}

// TestPrintPlanText_NoWarnWhenRemovedEmpty pins the case where
// --exclude is set but plan.Removed is empty: the exclude did
// not produce a destructive subset → no warn. The threshold
// is "exclude AND Removed non-empty", not just "exclude set".
func TestPrintPlanText_NoWarnWhenRemovedEmpty(t *testing.T) {
	plan := basePlan()
	plan.Removed = nil // --exclude did not drop any apps

	var buf bytes.Buffer
	exit := printPlanText(&buf, plan, []string{"checkout-web"}, false)
	if exit != 0 {
		t.Fatalf("printPlanText exit code: got %d, want 0", exit)
	}
	out := buf.String()
	if strings.Contains(out, "Applying will soft-delete") {
		t.Fatalf("unexpected warning when Removed is empty; output:\n%s", out)
	}
}

// TestPrintPlanText_WarnCountReflectsPlanRemoved pins the
// warning message's count semantics: it must reflect
// len(plan.Removed), not len(plan.Workloads) or any other
// scalar. A refactor that printed "Applying will soft-delete
// N workloads" using the wrong field would mislead the
// operator about the destructive scale.
func TestPrintPlanText_WarnCountReflectsPlanRemoved(t *testing.T) {
	plan := basePlan()
	plan.Removed = []string{"alpha", "beta", "delta"}

	var buf bytes.Buffer
	exit := printPlanText(&buf, plan, []string{"unrelated"}, false)
	if exit != 0 {
		t.Fatalf("printPlanText exit code: got %d, want 0", exit)
	}
	out := buf.String()
	if !strings.Contains(out, "3 app(s)") {
		t.Fatalf("warning count drift: got output %q, want substring %q", out, "3 app(s)")
	}
}

// rescuePlan returns the wire shape the server emits when the
// gate was blocked pre-exclude and --exclude rescued it. The
// wire invariant (cmd/apid/scan_service.go:864) is
// `gateRescuedByExclude := !preCanApply && canApply`, so
// CanApply=true + GateRescuedByExclude=true is the rescue case.
// Reasons carry the pre-exclude blocker list so the operator
// sees what would have failed without --exclude.
func rescuePlan() api.PlanResponse {
	p := basePlan()
	p.CanApplyPreExclude = false
	p.GateRescuedByExclude = true
	p.CanApplyReasons = []string{"plan_apps_over_limit"}
	p.Workloads = []api.PlanWorkload{{Name: "api", RootDir: "/api", Class: "http"}}
	return p
}

// TestPrintPlanText_RendersRescueLine pins ADR-124 follow-up #1:
// when the gate was blocked pre-exclude and --exclude rescued
// it, the operator sees the rescue line + the pre-exclude
// reason list BEFORE the can_apply: true line. A refactor that
// moved the rescue render to after can_apply would bury the
// signal in a workloads table the operator isn't reading yet.
func TestPrintPlanText_RendersRescueLine(t *testing.T) {
	var buf bytes.Buffer
	exit := printPlanText(&buf, rescuePlan(), []string{"checkout-api"}, false)
	if exit != 0 {
		t.Fatalf("printPlanText exit code: got %d, want 0", exit)
	}
	out := buf.String()
	if !strings.Contains(out, "Gate rescued by --exclude") {
		t.Fatalf("missing rescue header; output:\n%s", out)
	}
	if !strings.Contains(out, "plan_apps_over_limit") {
		t.Fatalf("missing pre-exclude reason; output:\n%s", out)
	}
	// The rescue line must appear BEFORE the can_apply: true line so
	// the operator sees it before reading the workloads table.
	rescueIdx := strings.Index(out, "Gate rescued by --exclude")
	canApplyIdx := strings.Index(out, "can_apply: true")
	if rescueIdx < 0 || canApplyIdx < 0 {
		t.Fatalf("missing one of the markers; got:\n%s", out)
	}
	if rescueIdx > canApplyIdx {
		t.Fatalf("rescue header appeared after can_apply: true (rescue=%d, can_apply=%d); output:\n%s",
			rescueIdx, canApplyIdx, out)
	}
}

// TestPrintPlanText_NoRescueLineWhenCanApplyFalseButNotRescued is
// the REGRESSION GUARD for a normal blocked plan. The
// PR-A followup is constrained to render the rescue line ONLY
// when GateRescuedByExclude=true; the !CanApply && !Rescued
// path (still-blocked gate) must NOT emit the rescue header —
// the operator sees "can_apply: false" + the existing partition
// view instead. A refactor that "simplified" the branch and
// rendered rescue info on any !CanApply path would surface
// confusing lines for non-rescue failures.
func TestPrintPlanText_NoRescueLineWhenCanApplyFalseButNotRescued(t *testing.T) {
	plan := basePlan()
	plan.CanApply = false             // post-exclude gate also blocks
	plan.CanApplyPreExclude = false   // pre-exclude gate was also blocked
	plan.GateRescuedByExclude = false // --exclude did NOT rescue
	plan.CanApplyReasons = []string{"plan_apps_over_limit"}

	var buf bytes.Buffer
	exit := printPlanText(&buf, plan, []string{"checkout-api"}, false)
	if exit != 0 {
		t.Fatalf("printPlanText exit code: got %d, want 0", exit)
	}
	out := buf.String()
	if strings.Contains(out, "Gate rescued by --exclude") {
		t.Fatalf("unexpected rescue line on still-blocked plan; output:\n%s", out)
	}
	if !strings.Contains(out, "can_apply: false") {
		t.Fatalf("missing can_apply: false; output:\n%s", out)
	}
	if !strings.Contains(out, "reason: plan_apps_over_limit") {
		t.Fatalf("missing blocked reason; output:\n%s", out)
	}
}

func TestPrintPlanText_BlockedPlanRetainsDetails(t *testing.T) {
	plan := basePlan()
	plan.CanApply = false
	plan.CanApplyReasons = []string{"apps over plan limit", "duplicate workload slug"}
	plan.Workloads = []api.PlanWorkload{{
		Name: "api", RootDir: "services/api", Class: "http",
		ServiceBindingPolicy:      api.ServiceBindingPolicyDeclared,
		PreviewServiceCallsPolicy: api.PreviewServiceCallsDeny,
	}}
	plan.Managed = []api.PlanManaged{{Name: "db", Kind: "postgres", EnvHint: "DATABASE_URL", Image: "postgres:17"}}
	plan.Warnings = []string{"ignored unsupported field"}

	var buf bytes.Buffer
	if exit := printPlanTextWithExplain(&buf, plan, nil, false, true); exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	out := buf.String()
	for _, want := range []string{
		"can_apply: false",
		"reason: apps over plan limit",
		"reason: duplicate workload slug",
		"Workloads:", "services/api", "class=http", "service_policy=declared", "preview_calls=deny",
		"Managed (not provisioned):", "DATABASE_URL", "postgres:17",
		"Warnings:", "ignored unsupported field",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("blocked render missing %q; output:\n%s", want, out)
		}
	}
}

func TestPrintPlanText_BlockedShowAffectedAndExplain(t *testing.T) {
	plan := basePlan()
	plan.CanApply = false
	plan.CanApplyReasons = []string{"apps over plan limit"}
	plan.Workloads = []api.PlanWorkload{{Name: "api", RootDir: "services/api", DetectedBy: &api.PlanDetectedBy{Detector: "procfile"}}}
	plan.WillDeploy = []api.PlanAffectedApp{{Slug: "api", Action: "create"}}
	plan.Unaffected = []api.PlanAffectedApp{{Slug: "existing", ID: "app-1", Action: "noop"}}

	var buf bytes.Buffer
	if exit := printPlanTextWithExplain(&buf, plan, nil, true, true); exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	out := buf.String()
	for _, want := range []string{"reason: apps over plan limit", "Will deploy:", "api", "Unaffected", "existing", "Detection trace:", "detected_by: procfile"} {
		if !strings.Contains(out, want) {
			t.Errorf("affected blocked render missing %q; output:\n%s", want, out)
		}
	}
}

// TestPrintPlanText_NoEarlyReturnWhenRescued pins the wire-
// invariant contract: when --exclude rescues the gate, CanApply
// is true (server: !preCanApply && canApply). printPlanText
// must NOT take the !CanApply early-return path on a rescued
// plan, or the operator sees an empty render. The workloads
// table is the post-rescue fingerprint of "render continued".
func TestPrintPlanText_NoEarlyReturnWhenRescued(t *testing.T) {
	var buf bytes.Buffer
	exit := printPlanText(&buf, rescuePlan(), []string{"checkout-api"}, false)
	if exit != 0 {
		t.Fatalf("printPlanText exit code: got %d, want 0", exit)
	}
	out := buf.String()
	if !strings.Contains(out, "Workloads:") {
		t.Fatalf("rescued plan did not reach the workloads table (early-return leaked); output:\n%s", out)
	}
}

// TestPrintPlanText_NoRescueLineOnHappyPath pins the negative
// case for the rescue render: a normal can_apply=true plan
// (no rescue, no warning) must NOT emit "Gate rescued by
// --exclude". This is the script-grep / CI-output stability
// test — every can_apply=true scan on the common path should
// produce identical output shape.
func TestPrintPlanText_NoRescueLineOnHappyPath(t *testing.T) {
	var buf bytes.Buffer
	exit := printPlanText(&buf, basePlan(), nil, false)
	if exit != 0 {
		t.Fatalf("printPlanText exit code: got %d, want 0", exit)
	}
	out := buf.String()
	if strings.Contains(out, "Gate rescued by --exclude") {
		t.Fatalf("rescue line appeared on a non-rescued plan; output:\n%s", out)
	}
}

// TestPlanProblem_GateBlocked pins the ADR-124 follow-up #1 4th
// branch of planProblem: when the gate was blocked pre-exclude
// AND post-exclude did not rescue, the wire code is
// CodePlanGateBlocked (a new constant) carrying the full
// can_apply_reasons list. A refactor that reused
// CodePlanLimitApps / CodePlanCronQuota for this case would
// silently break the dashboard's upsell-vs-quota copy because
// those codes drive specific render templates. The Test
// covers:
//  1. The 403 status is preserved (gate blocks at apply).
//  2. Code is CodePlanGateBlocked (the new constant).
//  3. Title + Detail carry the operator-visible signal.
//  4. Multi-reason join uses "; " between entries (matches
//     the textual convention of the server-side wire).
func TestPlanProblem_GateBlocked(t *testing.T) {
	plan := basePlan()
	plan.CanApply = false
	plan.CanApplyPreExclude = false
	plan.GateRescuedByExclude = false
	plan.CanApplyReasons = []string{"plan_apps_over_limit", "plan_crons_over_limit"}

	p := planProblem(plan)
	if p.Status != 403 {
		t.Errorf("planProblem Status: got %d, want 403", p.Status)
	}
	if p.Code != api.CodePlanGateBlocked {
		t.Errorf("planProblem Code: got %q, want %q", p.Code, api.CodePlanGateBlocked)
	}
	if !strings.Contains(p.Detail, "plan_apps_over_limit") {
		t.Errorf("planProblem Detail missing first reason; got %q", p.Detail)
	}
	if !strings.Contains(p.Detail, "plan_crons_over_limit") {
		t.Errorf("planProblem Detail missing second reason; got %q", p.Detail)
	}
	if !strings.Contains(p.Detail, "; ") {
		t.Errorf("planProblem Detail missing reason separator '; '; got %q", p.Detail)
	}
}

// TestPlanProblem_PreservesExistingOrdering pins the regression-
// guard ordering of the 4 branches: CronsNotAllowed wins over
// OverApps wins over GateBlocked (the new 4th branch) wins over
// the CronQuota fallback. A refactor that re-ordered these
// would change wire behaviour on the existing 3 code paths;
// existing dashboards key off the specific codes.
func TestPlanProblem_PreservesExistingOrdering(t *testing.T) {
	// CronsNotAllowed still wins, even with GateBlocked conditions.
	plan := basePlan()
	plan.CronsNotAllowed = true
	plan.CanApplyPreExclude = false
	plan.CanApplyReasons = []string{"some_other_reason"}

	p := planProblem(plan)
	if p.Code != api.CodePlanCronsNotAllowed {
		t.Errorf("CronsNotAllowed branch lost to GateBlocked; got code %q, want %q",
			p.Code, api.CodePlanCronsNotAllowed)
	}

	// OverApps still wins over GateBlocked.
	plan = basePlan()
	plan.ObservedApps = plan.LimitApps + 1
	plan.CanApplyPreExclude = false
	plan.CanApplyReasons = []string{"some_other_reason"}

	p = planProblem(plan)
	if p.Code != api.CodePlanLimitApps {
		t.Errorf("OverApps branch lost to GateBlocked; got code %q, want %q",
			p.Code, api.CodePlanLimitApps)
	}
}
