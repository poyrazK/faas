package approval

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261020085706 — which releases wait, where, and when they may go on.

var t0 = time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)

func testPolicy() *state.ReleaseApprovalPolicy {
	return &state.ReleaseApprovalPolicy{AppID: "app", RequiredApprovals: 2, AfterStage: 1, TimeoutMinutes: 60,
		Approvers: state.ReleaseApproversAdmin, CreatedAt: t0}
}

func candidate() state.Deployment {
	started := t0.Add(2 * time.Hour)
	return state.Deployment{ID: "dep", AppID: "app", Scope: api.DefaultEnvScope, Status: state.DeployLive, TrafficPercent: 1,
		CanaryStep: 0, CanaryTotalSteps: 4, CanaryStepStartedAt: &started, RolloutState: "rolling_out",
		CreatedAt: t0.Add(time.Hour), DeployedByUserID: "deployer"}
}

func TestGated(t *testing.T) {
	app := state.App{ID: "app", Slug: "shop"}
	completed := t0.Add(3 * time.Hour)
	for _, tc := range []struct {
		name   string
		policy *state.ReleaseApprovalPolicy
		app    state.App
		edit   func(*state.Deployment)
		want   bool
	}{
		{name: "candidate after the policy", policy: testPolicy(), app: app, want: true},
		{name: "no policy", app: app},
		{name: "created before the policy", policy: testPolicy(), app: app, edit: func(d *state.Deployment) { d.CreatedAt = t0.Add(-time.Minute) }},
		// adr: 20261021152201 — a scheduled release starts its rollout at its
		// time, so a policy set while it waited gates it.
		{name: "scheduled before the policy, started after it", policy: testPolicy(), app: app, edit: func(d *state.Deployment) {
			started := t0.Add(time.Minute)
			d.CreatedAt, d.RolloutStartedAt = t0.Add(-time.Hour), &started
		}, want: true},
		{name: "rolling out before the policy", policy: testPolicy(), app: app, edit: func(d *state.Deployment) {
			started := t0.Add(-time.Minute)
			d.CreatedAt, d.RolloutStartedAt = t0.Add(-time.Hour), &started
		}},
		{name: "completed a rollout", policy: testPolicy(), app: app, edit: func(d *state.Deployment) { d.RolloutCompletedAt = &completed }},
		{name: "serving release", policy: testPolicy(), app: app, edit: func(d *state.Deployment) { d.TrafficPercent = 100 }},
		{name: "named scope", policy: testPolicy(), app: app, edit: func(d *state.Deployment) { d.Scope = "staging" }},
		// adr: 20261020085706 amendment 1 — a project's production environment
		// is production, as it is for freezes.
		{name: "production environment", policy: testPolicy(), app: app, edit: func(d *state.Deployment) { d.Scope = "production" }, want: true},
		{name: "dark candidate", policy: testPolicy(), app: app, edit: func(d *state.Deployment) { d.TrafficPercent = 0; d.CanaryTotalSteps = 0 }, want: true},
		{name: "worker app", policy: testPolicy(), app: state.App{ID: "app", Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeWorker}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := candidate()
			if tc.edit != nil {
				tc.edit(&d)
			}
			if got := Gated(tc.policy, tc.app, d); got != tc.want {
				t.Fatalf("Gated = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWaitStep(t *testing.T) {
	for _, tc := range []struct {
		afterStage, totalSteps, want int
	}{
		{1, 4, 0}, {2, 4, 1}, {3, 4, 2}, {9, 4, 2}, {5, 2, 0}, {1, 0, 0},
	} {
		d := candidate()
		d.CanaryTotalSteps = tc.totalSteps
		if got := WaitStep(state.ReleaseApprovalPolicy{AfterStage: tc.afterStage}, d); got != tc.want {
			t.Fatalf("WaitStep(after %d, %d steps) = %d, want %d", tc.afterStage, tc.totalSteps, got, tc.want)
		}
	}
}

func TestStatusCountsEligibleApprovals(t *testing.T) {
	app := state.App{ID: "app", Slug: "shop"}
	approve := func(who string) state.ReleaseApproval {
		return state.ReleaseApproval{ApproverAccountID: who, Approver: who + "@example.com", ApproverRole: "admin",
			Decision: state.ReleaseApprovalApprove, CreatedAt: t0.Add(150 * time.Minute)}
	}
	now := t0.Add(150 * time.Minute)
	for _, tc := range []struct {
		name      string
		policy    func(*state.ReleaseApprovalPolicy)
		decisions []state.ReleaseApproval
		now       time.Time
		state     string
		approvals int
	}{
		{name: "nobody yet", now: now, state: api.ReleaseApprovalStateWaiting},
		{name: "deployer does not count", decisions: []state.ReleaseApproval{approve("deployer"), approve("ana")}, now: now,
			state: api.ReleaseApprovalStateWaiting, approvals: 1},
		{name: "deployer counts with self-approval", policy: func(p *state.ReleaseApprovalPolicy) { p.AllowSelfApproval = true },
			decisions: []state.ReleaseApproval{approve("deployer"), approve("ana")}, now: now, state: api.ReleaseApprovalStateApproved, approvals: 2},
		{name: "two others approve", decisions: []state.ReleaseApproval{approve("ana"), approve("bo")}, now: now,
			state: api.ReleaseApprovalStateApproved, approvals: 2},
		{name: "a rejection wins", decisions: []state.ReleaseApproval{approve("ana"), approve("bo"),
			{ApproverAccountID: "cy", Decision: state.ReleaseApprovalReject, Comment: "latency"}}, now: now,
			state: api.ReleaseApprovalStateRejected, approvals: 2},
		{name: "timeout passed", now: t0.Add(3 * time.Hour), state: api.ReleaseApprovalStateExpired},
		{name: "approved before the timeout passed stays approved", decisions: []state.ReleaseApproval{approve("ana"), approve("bo")},
			now: t0.Add(5 * time.Hour), state: api.ReleaseApprovalStateApproved, approvals: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy := testPolicy()
			if tc.policy != nil {
				tc.policy(policy)
			}
			got := Status(policy, app, candidate(), tc.decisions, tc.now)
			if got.State != tc.state || got.Approvals != tc.approvals || got.RequiredApprovals != 2 || len(got.Decisions) != len(tc.decisions) {
				t.Fatalf("status = %+v, want state %s with %d approvals", got, tc.state, tc.approvals)
			}
			if got.WaitingSince != "2026-10-20T11:00:00Z" || got.ExpiresAt != "2026-10-20T12:00:00Z" {
				t.Fatalf("wait = %s .. %s", got.WaitingSince, got.ExpiresAt)
			}
		})
	}
}

func TestStatusBeforeTheWaitingStage(t *testing.T) {
	policy := testPolicy()
	policy.AfterStage = 2
	got := Status(policy, state.App{ID: "app"}, candidate(), nil, t0.Add(10*time.Hour))
	if got.State != api.ReleaseApprovalStateWaiting || got.WaitingSince != "" || got.ExpiresAt != "" {
		t.Fatalf("status = %+v; a release before its waiting stage has no timeout yet", got)
	}
	if AdvanceBlocked(policy, candidate(), 0, got) {
		t.Fatal("advancing from stage 1 must not wait when the policy waits in stage 2")
	}
	if !AdvanceBlocked(policy, candidate(), 1, got) {
		t.Fatal("advancing from stage 2 must wait")
	}
	if Blocks(Status(nil, state.App{ID: "app"}, candidate(), nil, t0)) {
		t.Fatal("no policy must not block")
	}
}

func TestTakesTrafficWithoutCanary(t *testing.T) {
	zero, half := 0, 50
	for _, tc := range []struct {
		name string
		req  api.CreateDeploymentRequest
		want bool
	}{
		{name: "default immediate", want: true},
		{name: "canary", req: api.CreateDeploymentRequest{Canary: &api.CanaryPresetSpec{Preset: "adaptive"}}},
		{name: "canary none", req: api.CreateDeploymentRequest{Canary: &api.CanaryPresetSpec{Preset: "none"}}, want: true},
		{name: "dark", req: api.CreateDeploymentRequest{TrafficPercent: &zero}},
		{name: "manual split", req: api.CreateDeploymentRequest{TrafficPercent: &half}, want: true},
	} {
		if got := TakesTrafficWithoutCanary(&tc.req); got != tc.want {
			t.Fatalf("%s: TakesTrafficWithoutCanary = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestRoleAllowed(t *testing.T) {
	if !RoleAllowed(api.ReleaseApproversAdmin, state.OrgRoleAdmin) || RoleAllowed(api.ReleaseApproversAdmin, state.OrgRoleDeveloper) {
		t.Fatal("admin approvers: owners and admins only")
	}
	if !RoleAllowed(api.ReleaseApproversDeveloper, state.OrgRoleDeveloper) || RoleAllowed(api.ReleaseApproversDeveloper, state.OrgRoleViewer) {
		t.Fatal("developer approvers: developers too, never viewers")
	}
}

// adr: 20261021152201 — a release waiting without traffic cannot be given
// traffic by hand past an approval policy; it starts through a schedule.
func TestParkedNeedsCanary(t *testing.T) {
	app := state.App{ID: "app", Slug: "shop"}
	parked := state.Deployment{ID: "dep", Scope: api.DefaultEnvScope, Status: state.DeployLive, TrafficPercentExplicit: true}
	if !ParkedNeedsCanary(testPolicy(), app, parked) {
		t.Fatal("a parked production release of an app with a policy needs a canary")
	}
	if ParkedNeedsCanary(nil, app, parked) {
		t.Fatal("no policy, no requirement")
	}
	serving := parked
	serving.TrafficPercent = 100
	staging := parked
	staging.Scope = "staging"
	demoted := parked
	demoted.TrafficPercentExplicit = false
	for name, d := range map[string]state.Deployment{"serving": serving, "named scope": staging, "a release demoted to 0%": demoted} {
		if ParkedNeedsCanary(testPolicy(), app, d) {
			t.Fatalf("%s needs no canary", name)
		}
	}
	production := parked
	production.Scope = "production"
	if !ParkedNeedsCanary(testPolicy(), app, production) {
		t.Fatal("a parked release in the production environment needs a canary")
	}
}

type fakeReleaseReader struct {
	policy bool
	live   map[string]bool
}

func (f fakeReleaseReader) ReleaseApprovalPolicy(context.Context, string) (state.ReleaseApprovalPolicy, error) {
	if !f.policy {
		return state.ReleaseApprovalPolicy{}, state.ErrNotFound
	}
	return *testPolicy(), nil
}

func (f fakeReleaseReader) LiveDeploymentForScope(_ context.Context, _ string, scope string) (state.Deployment, error) {
	if !f.live[scope] {
		return state.Deployment{}, state.ErrNotFound
	}
	return state.Deployment{Scope: scope}, nil
}

// adr: 20261020085706 amendment 1 — a deploy into a project's production
// environment needs a canary like a default-scope one; other environments,
// and the first release in a scope, do not.
func TestCanaryRequired(t *testing.T) {
	app := state.App{ID: "app", Slug: "shop"}
	both := fakeReleaseReader{policy: true, live: map[string]bool{api.DefaultEnvScope: true, "production": true, "staging": true}}
	for _, tc := range []struct {
		name               string
		store              fakeReleaseReader
		scope, environment string
		want               bool
	}{
		{name: "default scope", store: both, want: true},
		{name: "explicit default scope", store: both, scope: api.DefaultEnvScope, want: true},
		{name: "production environment", store: both, environment: "production", want: true},
		{name: "resolved production scope", store: both, scope: "production", environment: "production", want: true},
		{name: "staging environment", store: both, environment: "staging"},
		{name: "named scope", store: both, scope: "staging"},
		{name: "no policy", store: fakeReleaseReader{live: both.live}, environment: "production"},
		{name: "first production release", store: fakeReleaseReader{policy: true, live: map[string]bool{api.DefaultEnvScope: true}}, environment: "production"},
	} {
		got, err := CanaryRequired(context.Background(), tc.store, app, tc.scope, tc.environment)
		if err != nil || got != tc.want {
			t.Fatalf("%s: CanaryRequired = %v, %v; want %v", tc.name, got, err, tc.want)
		}
	}
}
