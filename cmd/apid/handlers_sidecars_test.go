package main

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func withTestSidecarRecipient(t *testing.T) {
	t.Helper()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity: %v", err)
	}
	previousRecipient := setSidecarRecipient
	setSidecarRecipient = func() *age.X25519Recipient { return identity.Recipient() }
	t.Cleanup(func() { setSidecarRecipient = previousRecipient })
}

// goodSidecarImage is a placeholder valid image
// (sha256-pinned digest form, matches Sidecar.Validate's
// regex) so the test rows don't trip the per-element image
// gate before reaching the cap check.
const goodSidecarImage = "ghcr.io/me/x@sha256:" + "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899"

func TestBuildDeploymentForInsert_ServiceDefaultsToReadinessRollout(t *testing.T) {
	app := state.App{
		ID:       "app-service",
		Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeService},
	}
	dep, problem := buildDeploymentForInsert(app, &api.CreateDeploymentRequest{Image: "sha256:test"}, nil, testSidecarLimits())
	if problem != nil {
		t.Fatalf("buildDeploymentForInsert: %v", problem)
	}
	if !state.IsServiceRollout(dep) {
		t.Fatalf("deployment marker = state:%q canary_steps:%d; want readiness-gated service rollout", dep.RolloutState, dep.CanaryTotalSteps)
	}
	if dep.TrafficPercent != 0 || dep.RolloutStartedAt == nil {
		t.Fatalf("service rollout = traffic:%d started_at:%v; want traffic 0 and a start timestamp", dep.TrafficPercent, dep.RolloutStartedAt)
	}
}

func TestBuildDeploymentForInsert_SnapshotsRevisionResources(t *testing.T) {
	app := state.App{ID: "app-resources", RAMMB: 256, CPUMillicores: 500, MaxConcurrency: 1}
	ramMB, cpuMillicores := 128, 250
	dep, problem := buildDeploymentForInsert(app, &api.CreateDeploymentRequest{
		Image:     "sha256:test",
		Resources: &api.DeploymentResourcesRequest{RAMMB: &ramMB, CPUMillicores: &cpuMillicores},
	}, nil, testSidecarLimits(), api.PlanHobby)
	if problem != nil {
		t.Fatalf("buildDeploymentForInsert: %v", problem)
	}
	if dep.RAMMB != ramMB || dep.CPUMillicores != cpuMillicores {
		t.Fatalf("deployment resources = %d MiB/%d mCPU, want %d MiB/%d mCPU", dep.RAMMB, dep.CPUMillicores, ramMB, cpuMillicores)
	}
}

func TestBuildDeploymentForInsert_PersistsDeploymentMaxInstances(t *testing.T) {
	maxInstances := 2
	app := state.App{ID: "app-max-instances", MinInstances: 1}
	dep, problem := buildDeploymentForInsert(app, &api.CreateDeploymentRequest{
		Image: "sha256:test", MaxInstances: &maxInstances,
	}, nil, testSidecarLimits(), api.PlanPro)
	if problem != nil {
		t.Fatalf("buildDeploymentForInsert: %v", problem)
	}
	if dep.MaxInstances != maxInstances {
		t.Fatalf("deployment max_instances = %d, want %d", dep.MaxInstances, maxInstances)
	}
}

func TestBuildDeploymentForInsert_PersistsDeploymentCPUScalingTarget(t *testing.T) {
	target := 67.5
	app := state.App{ID: "app-cpu-target", RAMMB: 512, CPUMillicores: 500, MaxConcurrency: 5}
	dep, problem := buildDeploymentForInsert(app, &api.CreateDeploymentRequest{
		Image: "sha256:test", Scaling: &api.DeploymentScalingRequest{CPUUtilizationTargetPct: &target},
	}, nil, api.MustLimitsFor(api.PlanPro), api.PlanPro)
	if problem != nil {
		t.Fatalf("buildDeploymentForInsert: %v", problem)
	}
	if dep.CPUUtilizationTargetPct == nil || *dep.CPUUtilizationTargetPct != target {
		t.Fatalf("deployment CPU target = %v, want %v", dep.CPUUtilizationTargetPct, target)
	}
}

func TestBuildDeploymentForInsert_PersistsRevisionConcurrencyCap(t *testing.T) {
	maxRequests := 8
	dep, problem := buildDeploymentForInsert(state.App{ID: "app-concurrency-cap"}, &api.CreateDeploymentRequest{
		Image: "sha256:test", Scaling: &api.DeploymentScalingRequest{MaxConcurrentRequests: &maxRequests},
	}, nil, api.MustLimitsFor(api.PlanPro), api.PlanPro)
	if problem != nil {
		t.Fatalf("buildDeploymentForInsert: %v", problem)
	}
	if dep.MaxConcurrentRequests != maxRequests {
		t.Fatalf("deployment max_concurrent_requests = %d, want %d", dep.MaxConcurrentRequests, maxRequests)
	}
}

func TestValidateDeploymentScaling(t *testing.T) {
	belowMinimum := 0.5
	freeTarget := 70.0
	nanTarget := math.NaN()
	infiniteTarget := math.Inf(1)
	if problem := validateDeploymentScaling(&api.DeploymentScalingRequest{CPUUtilizationTargetPct: &belowMinimum}, api.PlanPro); problem == nil || problem.Code != api.CodeInvalidAutoscaleTargetCPU {
		t.Fatalf("below-minimum target problem = %#v, want %q", problem, api.CodeInvalidAutoscaleTargetCPU)
	}
	for name, value := range map[string]float64{"NaN": nanTarget, "infinity": infiniteTarget} {
		if problem := validateDeploymentScaling(&api.DeploymentScalingRequest{CPUUtilizationTargetPct: &value}, api.PlanPro); problem == nil || problem.Code != api.CodeInvalidAutoscaleTargetCPU {
			t.Errorf("%s target problem = %#v, want %q", name, problem, api.CodeInvalidAutoscaleTargetCPU)
		}
	}
	if problem := validateDeploymentScaling(&api.DeploymentScalingRequest{CPUUtilizationTargetPct: &freeTarget}, api.PlanFree); problem == nil || problem.Code != api.CodePlanScaleUpNotAllowed {
		t.Fatalf("Free target problem = %#v, want %q", problem, api.CodePlanScaleUpNotAllowed)
	}
	zero := 0.0
	if problem := validateDeploymentScaling(&api.DeploymentScalingRequest{CPUUtilizationTargetPct: &zero}, api.PlanPro); problem != nil {
		t.Fatalf("explicit zero should disable the target: %v", problem)
	}
	for name, value := range map[string]int{"zero": 0, "above plan limit": api.MustLimitsFor(api.PlanHobby).ConcurrencyPerVMBound + 1} {
		if problem := validateDeploymentScaling(&api.DeploymentScalingRequest{MaxConcurrentRequests: &value}, api.PlanHobby); problem == nil || problem.Code != api.CodeValidation {
			t.Errorf("%s concurrency cap problem = %#v, want %q", name, problem, api.CodeValidation)
		}
	}
	validCap := 1
	if problem := validateDeploymentScaling(&api.DeploymentScalingRequest{MaxConcurrentRequests: &validCap}, api.PlanFree); problem != nil {
		t.Fatalf("Free plan should permit a lower per-instance cap: %v", problem)
	}
}

func TestDeploymentScalingResponseIncludesRequestCap(t *testing.T) {
	response := deploymentScalingResponse(state.Deployment{MaxConcurrentRequests: 6})
	if response == nil || response.MaxConcurrentRequests == nil || *response.MaxConcurrentRequests != 6 {
		t.Fatalf("scaling response = %#v, want max_concurrent_requests=6", response)
	}
	if got := deploymentScalingResponse(state.Deployment{}); got != nil {
		t.Fatalf("inherited scaling response = %#v, want nil", got)
	}
}

func TestValidateDeploymentMaxInstances(t *testing.T) {
	proLimits := api.MustLimitsFor(api.PlanPro)
	freeLimits := api.MustLimitsFor(api.PlanFree)
	minInstances := 2
	tooMany := proLimits.MaxConcurrency + 1
	negative := -1
	belowFloor := 1
	valid := 2
	cases := []struct {
		name      string
		requested *int
		app       state.App
		plan      api.Plan
		limits    api.Limits
		wantCode  string
	}{
		{name: "omitted inherits", requested: nil, plan: api.PlanPro, limits: proLimits},
		{name: "zero inherits", requested: intPointer(0), plan: api.PlanPro, limits: proLimits},
		{name: "valid cap", requested: &valid, plan: api.PlanPro, limits: proLimits},
		{name: "free plan gate", requested: intPointer(1), plan: api.PlanFree, limits: freeLimits, wantCode: api.CodePlanMaxInstancesNotAllowed},
		{name: "negative", requested: &negative, plan: api.PlanPro, limits: proLimits, wantCode: api.CodeInvalidMaxInstances},
		{name: "above plan limit", requested: &tooMany, plan: api.PlanPro, limits: proLimits, wantCode: api.CodeInvalidMaxInstances},
		{name: "below reachable floor", requested: &belowFloor, app: state.App{MinInstances: minInstances}, plan: api.PlanPro, limits: proLimits, wantCode: api.CodeInvalidMaxInstances},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			problem := validateDeploymentMaxInstances(tc.requested, tc.app, tc.plan, tc.limits)
			if tc.wantCode == "" {
				if problem != nil {
					t.Fatalf("validateDeploymentMaxInstances: got %s, want success", problem.Code)
				}
				return
			}
			if problem == nil || problem.Code != tc.wantCode {
				t.Fatalf("validateDeploymentMaxInstances = %#v, want code %q", problem, tc.wantCode)
			}
		})
	}
}

func intPointer(value int) *int { return &value }

func TestResolveDeploymentResourcesRejectsInvalidOverrides(t *testing.T) {
	app := state.App{ID: "app-resources", RAMMB: 256, CPUMillicores: 500, MaxConcurrency: 1}
	conflictingRAM := 384
	tooMuchRAM := 512
	unsupportedCPU := 750
	cases := []struct {
		name      string
		requested *api.DeploymentResourcesRequest
	}{
		{
			name: "profile conflicts with explicit memory",
			requested: &api.DeploymentResourcesRequest{
				ResourceProfile: stringPointer("small"), RAMMB: &conflictingRAM,
			},
		},
		{
			name:      "memory exceeds plan",
			requested: &api.DeploymentResourcesRequest{RAMMB: &tooMuchRAM},
		},
		{
			name:      "unsupported CPU shape",
			requested: &api.DeploymentResourcesRequest{CPUMillicores: &unsupportedCPU},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, problem := resolveDeploymentResources(app, tc.requested, testSidecarLimits(), api.PlanHobby); problem == nil {
				t.Fatal("resolveDeploymentResources succeeded; want validation problem")
			}
		})
	}
}

func stringPointer(value string) *string { return &value }

func TestBuildDeploymentForInsert_PersistsMainDependencies(t *testing.T) {
	want := []api.WorkloadDependency{
		{Name: "proxy", Condition: api.WorkloadDependencyHealthy},
	}
	overrides := &api.CreateDeploymentOverrides{MainDependsOn: want}
	dep, problem := buildDeploymentForInsert(state.App{ID: "app-main-deps"}, &api.CreateDeploymentRequest{
		Image: "sha256:test", Overrides: overrides,
	}, overrides, testSidecarLimits(), api.PlanPro)
	if problem != nil {
		t.Fatalf("buildDeploymentForInsert: %v", problem)
	}
	var got []api.WorkloadDependency
	if err := json.Unmarshal(dep.OverrideMainDependsOn, &got); err != nil {
		t.Fatalf("unmarshal OverrideMainDependsOn: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("OverrideMainDependsOn = %+v, want %+v", got, want)
	}
}

func TestBuildDeploymentForInsert_FullRootfsPlanDefaultAndOverride(t *testing.T) {
	app := state.App{ID: "app-full-rootfs"}
	limits := testSidecarLimits()

	paid, problem := buildDeploymentForInsert(app, &api.CreateDeploymentRequest{Image: "sha256:test"}, nil, limits, api.PlanHobby)
	if problem != nil {
		t.Fatalf("paid buildDeploymentForInsert: %v", problem)
	}
	if !paid.FullRootfsAllowAuto {
		t.Fatal("paid plan should default full-rootfs auto-fallback on")
	}

	free, problem := buildDeploymentForInsert(app, &api.CreateDeploymentRequest{Image: "sha256:test"}, nil, limits, api.PlanFree)
	if problem != nil {
		t.Fatalf("free buildDeploymentForInsert: %v", problem)
	}
	if free.FullRootfsAllowAuto {
		t.Fatal("Free plan should default full-rootfs auto-fallback off")
	}

	falseValue := false
	trueValue := true
	explicit, problem := buildDeploymentForInsert(app, &api.CreateDeploymentRequest{
		Image: "sha256:test", FullRootfsAllowAuto: &falseValue, FullRootfsOverride: &trueValue,
	}, nil, limits, api.PlanHobby)
	if problem != nil {
		t.Fatalf("explicit buildDeploymentForInsert: %v", problem)
	}
	if explicit.FullRootfsAllowAuto || explicit.FullRootfsOverride == nil || !*explicit.FullRootfsOverride {
		t.Fatalf("explicit full-rootfs policy = auto:%t override:%v", explicit.FullRootfsAllowAuto, explicit.FullRootfsOverride)
	}
}

// testSidecarLimits returns the per-plan Limits table
// the apid gate reads (cmd/apid/main.go populates this at
// boot from pkg/api::planLimits via the exported LimitsFor
// accessor). Reused so the cap test exercises the same
// limits the handler sees in production.
func testSidecarLimits() api.Limits {
	l, _ := api.LimitsFor(api.PlanHobby)
	return l
}

func TestBuildDeploymentForInsert_PreservesExplicitZeroTraffic(t *testing.T) {
	zero := 0
	app := state.App{ID: "app", Manifest: state.AppManifest{}}
	dep, problem := buildDeploymentForInsert(app, &api.CreateDeploymentRequest{
		Image: "sha256:test", TrafficPercent: &zero,
	}, nil, testSidecarLimits(), api.PlanPro)
	if problem != nil {
		t.Fatalf("buildDeploymentForInsert: %v", problem)
	}
	if dep.TrafficPercent != 0 || !dep.TrafficPercentExplicit {
		t.Fatalf("traffic policy = %d explicit=%t, want 0/true", dep.TrafficPercent, dep.TrafficPercentExplicit)
	}
}

func TestBuildDeploymentForInsert_PreservesRollbackOn5xx(t *testing.T) {
	rollback := true
	app := state.App{ID: "app", Manifest: state.AppManifest{}}
	dep, problem := buildDeploymentForInsert(app, &api.CreateDeploymentRequest{
		Image: "sha256:test", RollbackOn5xx: &rollback,
	}, nil, testSidecarLimits(), api.PlanPro)
	if problem != nil {
		t.Fatalf("buildDeploymentForInsert: %v", problem)
	}
	if !dep.RollbackOn5xx {
		t.Fatal("deployment should preserve an explicit rollback_on_5xx=true opt-in")
	}
}

func TestBuildDeploymentForInsert_DisablesStartupCPUBoostWhenRequested(t *testing.T) {
	disable := true
	app := state.App{ID: "app", Manifest: state.AppManifest{}}
	dep, problem := buildDeploymentForInsert(app, &api.CreateDeploymentRequest{
		Image: "sha256:test", DisableStartupCPUBoost: &disable,
	}, nil, testSidecarLimits(), api.PlanPro)
	if problem != nil {
		t.Fatalf("buildDeploymentForInsert: %v", problem)
	}
	if !dep.DisableStartupCPUBoost {
		t.Fatal("deployment should preserve disable_startup_cpu_boost=true")
	}

	dep, problem = buildDeploymentForInsert(app, &api.CreateDeploymentRequest{Image: "sha256:test"}, nil, testSidecarLimits(), api.PlanPro)
	if problem != nil {
		t.Fatalf("buildDeploymentForInsert with omitted option: %v", problem)
	}
	if dep.DisableStartupCPUBoost {
		t.Fatal("omitted disable_startup_cpu_boost should preserve the default boost")
	}
}

// adr: 200
//
// ADR-200 opened first-wake 5xx auto-rollback to every plan, so Free and
// Hobby no longer collect plan_rollback_on_5xx_not_allowed here. The gate that
// remains is the fail-closed one: an unrecognised plan has no MustLimitsFor
// entry and must be refused rather than inheriting a permissive zero value.
func TestValidateDeploymentRollbackOptionsPlanGate(t *testing.T) {
	trueValue := true
	falseValue := false
	cases := []struct {
		name  string
		plan  api.Plan
		value *bool
		code  string
	}{
		{name: "free true is allowed", plan: api.PlanFree, value: &trueValue},
		{name: "hobby true is allowed", plan: api.PlanHobby, value: &trueValue},
		{name: "pro true is allowed", plan: api.PlanPro, value: &trueValue},
		{name: "scale true is allowed", plan: api.PlanScale, value: &trueValue},
		{name: "free false is allowed", plan: api.PlanFree, value: &falseValue},
		{name: "omitted is allowed", plan: api.PlanFree},
		{name: "unknown plan fails closed", plan: api.Plan("unknown"), value: &trueValue, code: api.CodePlanRollbackOn5xxNotAllowed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			problem := validateDeploymentRollbackOptions(&api.CreateDeploymentRequest{RollbackOn5xx: tc.value}, tc.plan)
			if tc.code == "" {
				if problem != nil {
					t.Fatalf("validateDeploymentRollbackOptions: got %+v, want nil", problem)
				}
				return
			}
			if problem == nil || problem.Code != tc.code || problem.Status != 403 {
				t.Fatalf("validateDeploymentRollbackOptions: got %+v, want 403/%s", problem, tc.code)
			}
		})
	}
}

// TestValidateAndPlanSidecars_SixHelpersRejected pins
// AC #3 of issue #463 / ADR-069 / PR-B at the apid
// handler level: a CreateDeploymentRequest carrying a
// 6-element companions array MUST surface the literal
// api.CodeSidecarCapExceeded via the handler's
// validateAndPlanSidecars gate. The earlier pkg/api DTO
// test pins the same wire code; this test confirms the
// handler chains it through to the *api.Problem that
// api.WriteProblem emits to the HTTP response.
//
// A regression that swaps the gate (e.g. removes the
// cap check, or changes the wire code to a near-synonym)
// fails this test in the same commit.
//
// Hobby is used because Hobby inherits the global five-helper cap
// (PR-A's accessor returns true for every plan; the
// load-bearing gate is the GLOBAL SidecarCapMax constant,
// not a per-plan matrix). The per-sidecar RamMB is set to
// 32 MB — well above the 16 MB floor — so the cap check
// fires first, not the ram_mb gate.
func TestValidateAndPlanSidecars_SixHelpersRejected(t *testing.T) {
	acct := state.Account{Plan: api.PlanHobby}
	limits := testSidecarLimits()
	req := &api.CreateDeploymentRequest{
		Sidecars: api.Sidecars{
			{Name: "a", Image: goodSidecarImage, Type: api.SidecarTypeInit, RamMB: 32},
			{Name: "b", Image: goodSidecarImage, Type: api.SidecarTypeSidecar, RamMB: 32},
			{Name: "c", Image: goodSidecarImage, Type: api.SidecarTypeSidecar, RamMB: 32},
			{Name: "d", Image: goodSidecarImage, Type: api.SidecarTypeSidecar, RamMB: 32},
			{Name: "e", Image: goodSidecarImage, Type: api.SidecarTypeSidecar, RamMB: 32},
			{Name: "f", Image: goodSidecarImage, Type: api.SidecarTypeSidecar, RamMB: 32},
		},
	}
	p := validateAndPlanSidecars(req, acct, limits)
	if p == nil {
		t.Fatal("validateAndPlanSidecars: expected Problem on 6-helper request, got nil")
	}
	if p.Code != api.CodeSidecarCapExceeded {
		t.Errorf("problem.Code = %q, want %q (RFC 7807 stable code, closed enum)",
			p.Code, api.CodeSidecarCapExceeded)
	}
	if p.Status != 400 {
		t.Errorf("problem.Status = %d, want 400", p.Status)
	}
	// Title must mention "sidecar" so the dashboard's
	// human-readable rendering stays useful (the wire
	// code is the load-bearing field; the title is the
	// UX hint).
	if !strings.Contains(strings.ToLower(p.Title), "sidecar") {
		t.Errorf("problem.Title = %q; want a substring mentioning sidecar", p.Title)
	}
}

// TestValidateAndPlanSidecars_FiveHelpersAccepted pins the
// maximum valid shape: one init plus four running companions.
func TestValidateAndPlanSidecars_FiveHelpersAccepted(t *testing.T) {
	acct := state.Account{Plan: api.PlanHobby}
	limits := testSidecarLimits()
	req := &api.CreateDeploymentRequest{
		Sidecars: api.Sidecars{
			{Name: "init", Image: goodSidecarImage, Type: api.SidecarTypeInit, RamMB: 32},
			{Name: "metrics", Image: goodSidecarImage, Type: api.SidecarTypeSidecar, RamMB: 32},
			{Name: "logs", Image: goodSidecarImage, Type: api.SidecarTypeSidecar, RamMB: 32},
			{Name: "proxy", Image: goodSidecarImage, Type: api.SidecarTypeSidecar, RamMB: 32},
			{Name: "tracing", Image: goodSidecarImage, Type: api.SidecarTypeSidecar, RamMB: 32},
		},
	}
	if p := validateAndPlanSidecars(req, acct, limits); p != nil {
		t.Errorf("validateAndPlanSidecars: expected nil on maximum five-helper request, got %+v", p)
	}
}

// TestValidateAndPlanSidecars_EmptySidecarsNoop pins the
// legacy tolerance: a request without sidecars MUST NOT
// trip the gate. A regression that always returns a
// Problem would block every pre-PR-B deploy.
func TestValidateAndPlanSidecars_EmptySidecarsNoop(t *testing.T) {
	acct := state.Account{Plan: api.PlanHobby}
	limits := testSidecarLimits()
	req := &api.CreateDeploymentRequest{}
	if p := validateAndPlanSidecars(req, acct, limits); p != nil {
		t.Errorf("validateAndPlanSidecars: expected nil on empty sidecars, got %+v", p)
	}
}

func TestValidateAndPlanSidecars_ValidatesPrimaryDependencies(t *testing.T) {
	acct := state.Account{Plan: api.PlanHobby}
	limits := testSidecarLimits()
	proxy := api.Sidecar{Name: "proxy", Image: goodSidecarImage, Type: api.SidecarTypeSidecar}
	cases := []struct {
		name string
		req  *api.CreateDeploymentRequest
		want string
	}{
		{
			name: "healthy-companion",
			req: &api.CreateDeploymentRequest{
				Overrides: &api.CreateDeploymentOverrides{MainDependsOn: []api.WorkloadDependency{
					{Name: "proxy", Condition: api.WorkloadDependencyHealthy},
				}},
				Sidecars: api.Sidecars{proxy},
			},
		},
		{
			name: "unknown-companion",
			req: &api.CreateDeploymentRequest{
				Overrides: &api.CreateDeploymentOverrides{MainDependsOn: []api.WorkloadDependency{{Name: "missing"}}},
			},
			want: "unknown companion",
		},
		{
			name: "cycle-through-main",
			req: &api.CreateDeploymentRequest{
				Overrides: &api.CreateDeploymentOverrides{MainDependsOn: []api.WorkloadDependency{{Name: "proxy"}}},
				Sidecars: api.Sidecars{{Name: "proxy", Image: goodSidecarImage, Type: api.SidecarTypeSidecar,
					DependsOn: []api.WorkloadDependency{{Name: "main"}}}},
			},
			want: "cycle",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			problem := validateAndPlanSidecarsWithImages(tc.req, acct, limits, nil)
			if tc.want == "" {
				if problem != nil {
					t.Fatalf("validateAndPlanSidecars: %v", problem)
				}
				return
			}
			if problem == nil || !strings.Contains(strings.ToLower(problem.Detail), tc.want) {
				t.Fatalf("problem = %+v, want detail containing %q", problem, tc.want)
			}
		})
	}
}

func TestValidateAndPlanSidecars_ResolvesManagedCompanion(t *testing.T) {
	req := &api.CreateDeploymentRequest{Companions: api.Companions{{
		Name: "otel-collector", Preset: "opentelemetry", Type: api.SidecarTypeSidecar, Port: 4318,
	}}}
	p := validateAndPlanSidecarsWithImages(req, state.Account{Plan: api.PlanFree}, testSidecarLimits(), map[string]string{
		"opentelemetry": goodSidecarImage,
	})
	if p != nil {
		t.Fatalf("validate managed companion: %+v", p)
	}
	if len(req.Companions) != 0 || len(req.Sidecars) != 1 || req.Sidecars[0].Image != goodSidecarImage {
		t.Fatalf("normalized request = %+v", req)
	}
}

func TestValidateAndPlanSidecars_ManagedCompanionUnavailable(t *testing.T) {
	req := &api.CreateDeploymentRequest{Companions: api.Companions{{
		Name: "otel-collector", Preset: "opentelemetry", Type: api.SidecarTypeSidecar, Port: 4318,
	}}}
	p := validateAndPlanSidecarsWithImages(req, state.Account{Plan: api.PlanFree}, testSidecarLimits(), nil)
	if p == nil || p.Code != api.CodeCompanionPresetUnavailable || p.Status != 503 {
		t.Fatalf("problem = %+v, want 503/%s", p, api.CodeCompanionPresetUnavailable)
	}
}
