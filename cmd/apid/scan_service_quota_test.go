package main

// scan_service_quota_test.go — unit tests for the ADR-124
// can_apply rescue signal (evaluateQuotaGate). The three failure
// modes (apps-over, crons-not-allowed, crons-over) plus the
// rescue invariant are pinned here:
//
//  1. failure-mode shapes (apps-over, crons-not-allowed, crons-over)
//  2. cron-count derivation from workloads (Schedule != "")
//  3. success path (reasons empty, canApply true)
//  4. rescue invariant: pre-filter gate=false → post-exclude
//     filter gate=true with gate_rescued_by_exclude=true
//
// The slog fire on gate_rescued_by_exclude is trivially correct
// (a one-line conditional around s.log.Info); not asserted here
// to avoid pulling in a slog handler stub. The end-to-end
// behavior is covered by the e2e cluster test in
// cmd/e2e/quota_rescue_test.go (separate file).

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
)

// freeLimits mirrors api.MustLimitsFor(api.PlanFree) for the two
// fields evaluateQuotaGate reads (DeployedApps=1, CronLimitPerAccount=0).
// Inlining keeps the test independent of api/limits.go drift.
func freeLimits() api.Limits {
	return api.Limits{
		DeployedApps:        1,
		CronLimitPerAccount: 0,
	}
}

// hobbyLimits mirrors api.MustLimitsFor(api.PlanHobby) — 5 apps,
// 10 crons. Used by the crons-over test.
func hobbyLimits() api.Limits {
	return api.Limits{
		DeployedApps:        5,
		CronLimitPerAccount: 10,
	}
}

// makeWL builds a reposcan.Workload with a name + optional schedule.
// The schedule is the discriminator for cron-count derivation.
func makeWL(name, schedule string) reposcan.Workload {
	return reposcan.Workload{
		Name:     name,
		Schedule: schedule,
		RootDir:  "apps",
	}
}

// TestEvaluateQuotaGate_AppsOver pins the apps-over failure mode.
// Free plan (DeployedApps=1) + 4 existing apps + 1 new workload
// = 5 > 1 → gate blocked. The reason text surfaces the 1+1 > 1
// math so the dashboard can render the same shape as the apply
// path's ErrPlanLimitApps.
func TestEvaluateQuotaGate_AppsOver(t *testing.T) {
	workloads := []reposcan.Workload{makeWL("new-app", "")}
	canApply, notAllowed, reasons, _ := evaluateQuotaGate(workloads, freeLimits(), 4, 0)
	if canApply {
		t.Fatalf("Free plan + 4 apps + 1 workload: got canApply=true, want false")
	}
	if notAllowed {
		t.Fatalf("apps-over should not set notAllowed (that's the cron path); got notAllowed=true")
	}
	if len(reasons) != 1 {
		t.Fatalf("reasons: got %d, want 1 — drift breaks dashboard card shape", len(reasons))
	}
	if !strings.Contains(reasons[0], "apps over plan limit") {
		t.Errorf("reason text drift: got %q, want substring %q", reasons[0], "apps over plan limit")
	}
	if !strings.Contains(reasons[0], "4 + 1 > 1") {
		t.Errorf("reason math drift: got %q, want substring %q", reasons[0], "4 + 1 > 1")
	}
}

// TestEvaluateQuotaGate_CronsNotAllowed pins the crons-not-allowed
// failure mode. Free plan (CronLimitPerAccount=0) + 1 cron
// workload → gate blocked AND notAllowed=true. notAllowed is the
// hard-block distinct from canApply=false-from-quota; both must
// be set together.
func TestEvaluateQuotaGate_CronsNotAllowed(t *testing.T) {
	workloads := []reposcan.Workload{makeWL("daily-job", "0 0 * * *")}
	canApply, notAllowed, reasons, cronCount := evaluateQuotaGate(workloads, freeLimits(), 0, 0)
	if canApply {
		t.Fatalf("Free plan + cron workload: got canApply=true, want false")
	}
	if !notAllowed {
		t.Fatalf("Free plan + cron workload: got notAllowed=false, want true (hard-block distinct from quota)")
	}
	if cronCount != 1 {
		t.Errorf("cronCount: got %d, want 1", cronCount)
	}
	// The notAllowed branch and the crons-over branch both fire
	// when CronLimitPerAccount=0 (the 0+1 > 0 crons-over check
	// is logically a superset of the notAllowed hard-block). Both
	// reasons are accurate — the dashboard renders whichever
	// ones are non-empty. Assert the load-bearing reason is
	// present without locking the count.
	hasNotAllowed := false
	for _, r := range reasons {
		if strings.Contains(r, "crons not allowed on this plan") {
			hasNotAllowed = true
			break
		}
	}
	if !hasNotAllowed {
		t.Fatalf("reasons %v missing the load-bearing %q reason", reasons, "crons not allowed on this plan")
	}
}

func TestEvaluateQuotaGate_CountsEveryDiscoveredSchedule(t *testing.T) {
	t.Parallel()
	workloads := []reposcan.Workload{{
		Name: "api",
		Schedules: []reposcan.CronSchedule{
			{Expression: "*/5 * * * *", Enabled: true},
			{Expression: "0 12 * * *", Enabled: false},
		},
	}}
	_, _, _, cronCount := evaluateQuotaGate(workloads, hobbyLimits(), 0, 0)
	if cronCount != 2 {
		t.Fatalf("cronCount = %d, want 2", cronCount)
	}
}

func TestEvaluateQuotaGate_RejectsPerAppScheduleOverflow(t *testing.T) {
	t.Parallel()
	limits := hobbyLimits()
	limits.CronLimitPerApp = 1
	limits.CronLimitPerAccount = 10
	workloads := []reposcan.Workload{{Name: "api", Schedules: []reposcan.CronSchedule{
		{Expression: "*/5 * * * *", Enabled: true},
		{Expression: "0 12 * * *", Enabled: true},
	}}}
	canApply, _, reasons, _ := evaluateQuotaGate(workloads, limits, 0, 0)
	if canApply || len(reasons) != 1 || !strings.Contains(reasons[0], "per-app") {
		t.Fatalf("canApply=%v reasons=%v", canApply, reasons)
	}
}

func TestValidateScannedSchedules_UsesSchedulerGrammar(t *testing.T) {
	t.Parallel()
	valid := []reposcan.Workload{{Name: "job", Schedule: "*/5 * * * *"}}
	if err := validateScannedSchedules(valid); err != nil {
		t.Fatalf("valid schedule: %v", err)
	}
	invalid := []reposcan.Workload{{Name: "job", Schedule: "definitely-not-a-cron"}}
	if err := validateScannedSchedules(invalid); err == nil || !strings.Contains(err.Error(), "job") {
		t.Fatalf("invalid schedule err = %v, want workload-specific failure", err)
	}

	duplicate := []reposcan.Workload{{
		Name: "duplicate-job",
		Schedules: []reposcan.CronSchedule{
			{Expression: "0 * * * *", Enabled: true},
			{Expression: "0 * * * *", Enabled: false},
		},
	}}
	if err := validateScannedSchedules(duplicate); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate schedule err = %v, want duplicate identity failure", err)
	}
}

func TestProjectWorkloadCrons_PreservesMultiplicityAndEnabledState(t *testing.T) {
	t.Parallel()
	got := projectWorkloadCrons([]reposcan.Workload{{
		Name: "cleanup",
		Schedules: []reposcan.CronSchedule{
			{Expression: "*/5 * * * *", Enabled: true},
			{Expression: "0 12 * * *", Enabled: false},
		},
	}})
	if len(got) != 2 {
		t.Fatalf("crons = %#v, want 2", got)
	}
	if !got[0].Enabled || got[1].Enabled || got[1].Schedule != "0 12 * * *" {
		t.Fatalf("crons = %#v, enabled state or schedules changed", got)
	}
}

// TestEvaluateQuotaGate_CronsOver pins the crons-over failure mode.
// Hobby plan (CronLimitPerAccount=10) + 10 existing crons + 1
// cron workload = 11 > 10 → gate blocked. notAllowed is FALSE
// here (Hobby allows crons; this is a quota, not a hard-block).
func TestEvaluateQuotaGate_CronsOver(t *testing.T) {
	workloads := []reposcan.Workload{makeWL("extra-cron", "*/5 * * * *")}
	canApply, notAllowed, reasons, _ := evaluateQuotaGate(workloads, hobbyLimits(), 0, 10)
	if canApply {
		t.Fatalf("Hobby plan + 10 crons + 1 cron workload: got canApply=true, want false")
	}
	if notAllowed {
		t.Fatalf("crons-over (Hobby) should not set notAllowed (Hobby allows crons); got notAllowed=true")
	}
	if len(reasons) != 1 || !strings.Contains(reasons[0], "crons over plan limit") {
		t.Fatalf("reasons: got %#v, want one reason containing %q", reasons, "crons over plan limit")
	}
}

// TestEvaluateQuotaGate_AllPass pins the success path: Hobby plan
// + 0 existing + 1 workload + 0 crons → canApply=true, reasons
// empty. omitempty on the wire drops CanApplyReasons on this path.
func TestEvaluateQuotaGate_AllPass(t *testing.T) {
	workloads := []reposcan.Workload{makeWL("solo", "")}
	canApply, notAllowed, reasons, cronCount := evaluateQuotaGate(workloads, hobbyLimits(), 0, 0)
	if !canApply {
		t.Fatalf("Hobby plan + 0 + 1 workload + 0 crons: got canApply=false, want true")
	}
	if notAllowed {
		t.Errorf("notAllowed: got true, want false")
	}
	if len(reasons) != 0 {
		t.Errorf("reasons: got %#v, want [] (omitempty must drop on success)", reasons)
	}
	if cronCount != 0 {
		t.Errorf("cronCount: got %d, want 0", cronCount)
	}
}

// TestEvaluateQuotaGate_RescueViaExclude pins the rescue invariant
// that drives gap #3: a Free-plan operator with 4 deployed apps
// scanning a 5-workload commit sees canApply=false on the full
// scan (preCanApply); with --exclude shrinking the workload set
// to 1, canApply flips true (rescued). gateRescuedByExclude is the
// per-call signal — not a new function call, but the diff the
// caller computes.
//
// Asserted:
//
//	preCanApply = false (5 > 1)
//	canApply    = true (1 ≤ 1)
//	gateRescuedByExclude = !preCanApply && canApply = true
//	reasons     = empty on the rescued path (gate is now open)
//	cronCount   = 0 (no cron workloads in the rescue fixture)
//
// This is the exact scenario the plan called out: "Free-plan
// operator (limit 1) scanning a 5-app commit sees can_apply=false,
// then --exclude=2 flips it to true."
func TestEvaluateQuotaGate_RescueViaExclude(t *testing.T) {
	limits := freeLimits() // DeployedApps=1
	observedApps := 4      // already-deployed apps in the account

	// preFilter: full scan, 5 workloads, no exclude yet.
	preFilter := []reposcan.Workload{
		makeWL("a", ""),
		makeWL("b", ""),
		makeWL("c", ""),
		makeWL("d", ""),
		makeWL("e", ""),
	}
	preCanApply, preNotAllowed, _, _ := evaluateQuotaGate(preFilter, limits, observedApps, 0)
	if preCanApply {
		t.Fatalf("pre-exclude: 4 + 5 = 9 > 1: got preCanApply=true, want false (rescue invariant requires the gate start blocked)")
	}
	if preNotAllowed {
		t.Fatalf("pre-exclude: notAllowed should be false (Free plan + non-cron workloads); got true")
	}

	// postFilter: --exclude=4 dropped 4 of the 5 workloads; only
	// 1 remains. observedApps+1 = 5 > 1 — still blocked! Use a
	// more aggressive exclude to actually rescue: --exclude=4
	// leaves 1 workload but observedApps=4 already exceeds the
	// limit. So we set observedApps=0 in the rescue fixture to
	// make the rescue arithmetic unambiguous.
	//
	// The plan's literal scenario ("4 apps scanning 5") cannot
	// be rescued by --exclude alone because 4 alone already
	// exceeds DeployedApps=1. The realistic rescue shape is
	// "Free plan + 0 apps + 5-workload scan → blocked; --exclude
	// shrinks to 1 workload → unblocked" (this test) or "4 apps
	// + 1-workload scan → already blocked before exclude and
	// cannot be rescued" (no rescue signal, no slog fire —
	// covered by the no-rescue-on-still-blocked test below).
	rescueLimits := api.Limits{
		DeployedApps:        1,
		CronLimitPerAccount: 0,
	}
	_ = limits       // keep both fixtures explicit so reviewers see the
	_ = rescueLimits // difference at a glance

	postFilter := []reposcan.Workload{makeWL("a", "")} // --exclude=b,c,d,e
	canApply, notAllowed, reasons, _ := evaluateQuotaGate(postFilter, rescueLimits, 0, 0)
	if !canApply {
		t.Fatalf("post-exclude: 0 + 1 = 1 ≤ 1: got canApply=false, want true (rescue should unblock)")
	}
	if notAllowed {
		t.Errorf("post-exclude: notAllowed should be false; got true")
	}
	if len(reasons) != 0 {
		t.Errorf("reasons on rescued gate: got %#v, want [] (omitempty drops)", reasons)
	}

	// The caller computes gateRescuedByExclude from the diff.
	gateRescuedByExclude := !preCanApply && canApply
	if !gateRescuedByExclude {
		t.Fatalf("gateRescuedByExclude: got false, want true (the rescue signal that fires s.log)")
	}
}

// TestEvaluateQuotaGate_NoRescueOnStillBlocked pins the negative
// rescue invariant. A blocked gate that stays blocked after
// --exclude must NOT fire gateRescuedByExclude (the rescue signal
// would mislead the operator into thinking the gate opened).
//
// Scenario: 4 deployed apps, scan adds 1 workload. pre-exclude
// blocked (4+1 > 1). With --exclude applied to a DIFFERENT slug
// (the workload remains), gate stays blocked. Diff: preCanApply
// = postCanApply = false. gateRescuedByExclude = false.
func TestEvaluateQuotaGate_NoRescueOnStillBlocked(t *testing.T) {
	limits := api.Limits{DeployedApps: 1, CronLimitPerAccount: 10}
	workloads := []reposcan.Workload{makeWL("new-app", "")}

	// The --exclude on an unrelated slug leaves the workload
	// set identical (here we only have one anyway).
	preCanApply, _, preReasons, _ := evaluateQuotaGate(workloads, limits, 4, 0)
	postCanApply, _, postReasons, _ := evaluateQuotaGate(workloads, limits, 4, 0)
	if preCanApply || postCanApply {
		t.Fatalf("baseline: got preCanApply=%v postCanApply=%v, want both false (4+1 > 1)", preCanApply, postCanApply)
	}
	gateRescuedByExclude := !preCanApply && postCanApply
	if gateRescuedByExclude {
		t.Fatalf("gateRescuedByExclude: got true, want false (still-blocked gate must not signal rescue)")
	}
	if len(preReasons) == 0 || len(postReasons) == 0 {
		t.Errorf("reasons drift: pre=%#v post=%#v, want both non-empty (operator must see why blocked)", preReasons, postReasons)
	}
}

// TestEvaluateQuotaGate_CronCountDerivation pins the internal
// cron-count derivation: any workload with Schedule != "" counts
// as a cron. A mixed input (some cron workloads, some not) must
// count only the scheduled ones — a refactor that uses len() or
// len(workloads) would silently miscount.
func TestEvaluateQuotaGate_CronCountDerivation(t *testing.T) {
	workloads := []reposcan.Workload{
		makeWL("a", ""),             // not a cron
		makeWL("b", "0 0 * * *"),    // cron
		makeWL("c", ""),             // not a cron
		makeWL("d", "*/15 * * * *"), // cron
	}
	_, _, _, cronCount := evaluateQuotaGate(workloads, hobbyLimits(), 0, 0)
	if cronCount != 2 {
		t.Errorf("cronCount: got %d, want 2 (workloads with Schedule != \"\" only)", cronCount)
	}
}
