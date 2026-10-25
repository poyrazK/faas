//go:build metal

// release_guards_drill_metal_test.go — the release guards through the real
// wire, on top of the safe-release drill's harness: deploy freezes
// (ADR-20261019071733), release gates on customer signals
// (ADR-20261019071734), release approvals and their notifications
// (ADR-20261020085706) and scheduled releases (ADR-20261021152201).
//
// apid enforces freezes, approvals and schedules; meterd's canary progression
// judges gates and delivers the release notices; gatewayd-internal records
// the new error groups the new_errors gate reads; schedd and vmmd run the
// revisions. Notices go to an email channel, and the harness's log mail
// transport writes each one to meterd's log.
//
// Build tag: metal. Requires /dev/kvm, root, Firecracker, FAAS_TEST_KERNEL.
// Wall clock is about 30 minutes; run with -timeout 90m.

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestReleaseGuardsDrillMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping metal release guards drill")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	builderImg, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	e2etest.OverrideBuilderBase(t, registry.AddImage("onebox-faas/builder-base", builderImg))
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", safeReleaseDrillBody)
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")

	image := func(repo string, args ...string) string {
		img, _ := e2etest.HelloImageAboveBaseWithArgs(repo, safeReleaseDrillBody, append([]string{"-addr", ":8080"}, args...)...)
		return registry.AddImage(repo, img)
	}
	v1Img, _ := e2etest.HelloImageAboveBase("library/rg-v1", safeReleaseDrillBody)
	v1 := registry.AddImage("library/rg-v1", v1Img)
	v2 := image("library/rg-v2")
	v3 := image("library/rg-v3", "-delay-root", "1ms")
	// Releases that answer real API traffic with an error the platform's own
	// 5xx and latency rules never see; each status is a new error group.
	reject422 := image("library/rg-reject-422", "-reject-api", "422")
	reject409 := image("library/rg-reject-409", "-reject-api", "409")

	prom := newSafeReleasePromStub(t)
	operator := freeTCPAddr(t)
	h := e2etest.Start(t, pool, e2etest.DeployWake|e2etest.Meterd,
		"FAAS_CANARY_PROGRESSION_TOKEN="+strings.Repeat("c", 40),
		"FAAS_SAFEDEPLOY_TOKEN="+strings.Repeat("s", 40),
		"FAAS_APID_METRICS_ADDR="+operator,
		"FAAS_APID_INTERNAL_BASE_URL=http://"+operator,
		"FAAS_PROMETHEUS_URL="+prom.URL(),
		"FAAS_REQUEST_TELEMETRY_ENABLED=true",
		// The new_errors gate reads the error groups the gateway records
		// (ADR-096); the harness disables that path by default. A per-test
		// socket keeps apid off /run/faas.
		"FAAS_APP_ERRORS_ENABLED=true",
		"FAAS_APID_APP_ERRORS_SOCKET="+filepath.Join(t.TempDir(), "app_errors.sock"),
	)
	prom.setTargets(h.ScheddMetricsURL, h.GatewayControlURL+"/metrics")
	if err := h.SetAPIDEnv("FAAS_ROUTE_PROBE_URL", h.GatewayURL); err != nil {
		t.Fatal(err)
	}
	if err := h.RestartAPID(); err != nil {
		t.Fatalf("restart apid with route probes: %v", err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
	})
	ctx := context.Background()
	store := state.NewPgStore(pool)
	d := &safeReleaseDrill{t: t, h: h, store: store, key: h.SeedAccount(ctx, api.PlanHobby, "guards")}
	d.waitLease(true, 2*time.Minute)
	g := &guardsDrill{d: d, pool: store}

	// One email channel carries every release notice in the drill. Email
	// channels may only send to the account's own address.
	const channel = "guards-mail"
	g.mustStatus(t, d.key, http.MethodPost, "/v1/notification-channels",
		api.CreateNotificationChannelRequest{Name: channel, Kind: "email", Email: guardsDeployerEmail}, http.StatusCreated, nil)

	t.Run("freeze refuses production releases until overridden or lifted, never rollbacks", func(t *testing.T) {
		const app = "rg-freeze"
		d.createApp(t, app)
		first := d.deployLive(t, app, api.CreateDeploymentRequest{Image: v1})
		second := d.deployLive(t, app, api.CreateDeploymentRequest{Image: v2, Canary: &api.CanaryPresetSpec{Preset: "none"}})
		d.waitDeployment(t, second.ID, 2*time.Minute, func(dep state.Deployment) bool { return dep.TrafficPercent == 100 })

		var freeze api.ReleaseFreeze
		g.mustStatus(t, d.key, http.MethodPost, "/v1/freezes",
			api.CreateReleaseFreezeRequest{App: app, Kind: api.ReleaseFreezeKindWindow, Reason: "drill freeze"}, http.StatusCreated, &freeze)
		var status api.ReleaseFreezeStatus
		g.mustStatus(t, d.key, http.MethodGet, "/v1/apps/"+app+"/freeze-status", nil, http.StatusOK, &status)
		if !freeze.Active || !status.Frozen || len(status.Active) != 1 {
			t.Fatalf("freeze = %+v status = %+v, want one active freeze", freeze, status)
		}

		before := g.deploymentCount(t, first.AppID)
		raw, code := doReq(t, h, d.key, http.MethodPost, "/v1/apps/"+app+"/deployments", api.CreateDeploymentRequest{Image: v3})
		if code != http.StatusConflict || !strings.Contains(string(raw), api.CodeReleaseFrozen) || !strings.Contains(string(raw), "drill freeze") {
			t.Fatalf("frozen deploy: status=%d body=%s, want 409 %s naming the freeze", code, raw, api.CodeReleaseFrozen)
		}
		if after := g.deploymentCount(t, first.AppID); after != before {
			t.Fatalf("a refused release created a deployment: %d -> %d", before, after)
		}

		// Rollbacks are never frozen.
		raw, code = doReq(t, h, d.key, http.MethodPost, "/v1/apps/"+app+"/rollback", map[string]any{"target_deployment_id": first.ID})
		if code/100 != 2 {
			t.Fatalf("rollback during a freeze: status=%d body=%s, want accepted", code, raw)
		}
		d.waitDeployment(t, first.ID, 5*time.Minute, func(dep state.Deployment) bool {
			return dep.Status == state.DeployLive && dep.TrafficPercent == 100
		})
		d.waitServing(t, app, time.Minute)

		overridden := d.deployLive(t, app, api.CreateDeploymentRequest{Image: v3, Canary: &api.CanaryPresetSpec{Preset: "none"}, OverrideFreeze: "drill hotfix"})
		g.waitAudit(t, "release.freeze_overridden", "drill hotfix")
		t.Logf("DRILL freeze_override deployment=%s", overridden.ID)

		g.mustStatus(t, d.key, http.MethodDelete, "/v1/freezes/"+freeze.ID, nil, http.StatusOK, nil)
		g.mustStatus(t, d.key, http.MethodGet, "/v1/apps/"+app+"/freeze-status", nil, http.StatusOK, &status)
		if status.Frozen {
			t.Fatalf("freeze status after lift = %+v, want not frozen", status)
		}
		lifted := d.deployLive(t, app, api.CreateDeploymentRequest{Image: v2, Canary: &api.CanaryPresetSpec{Preset: "none"}})
		t.Logf("DRILL freeze_lifted deployment=%s", lifted.ID)
	})

	t.Run("release that introduces a new error group fails its gate and the canary aborts", func(t *testing.T) {
		const app = "rg-gate"
		d.createApp(t, app)
		stable := d.deployLive(t, app, api.CreateDeploymentRequest{Image: v1})
		d.waitServing(t, app, time.Minute)
		zero := 0
		g.mustStatus(t, d.key, http.MethodPost, "/v1/apps/"+app+"/release-gates",
			api.CreateReleaseGateRequest{Name: "no_new_errors", Kind: "new_errors", MaxNewGroups: &zero, MinEvents: 3}, http.StatusCreated, nil)

		candidate := d.deployLive(t, app, api.CreateDeploymentRequest{Image: reject422, Canary: customCanary("50", "10m")})
		stop := d.sendTraffic(app, "/api", 500*time.Millisecond)
		started := time.Now()
		got := d.waitDeployment(t, candidate.ID, 6*time.Minute, func(dep state.Deployment) bool {
			return dep.RolloutState == "aborted" || dep.RolloutState == "complete"
		})
		stop()
		why := d.decision(t, got)
		t.Logf("DRILL gate_abort seconds=%.0f state=%q rule=%s gates=%+v summary=%q",
			time.Since(started).Seconds(), got.RolloutState, why.Rule, why.Gates, why.Summary)
		if got.RolloutState != "aborted" || why.Rule != api.RolloutRuleAbortReleaseGate {
			t.Fatalf("gated candidate = state:%q rule:%s, want aborted by %s", got.RolloutState, why.Rule, api.RolloutRuleAbortReleaseGate)
		}
		failed := failedGate(why.Gates, "no_new_errors")
		if failed == nil || failed.Observed < 1 || len(failed.ErrorClasses) == 0 {
			t.Fatalf("gate results = %+v, want no_new_errors failed with its error classes", why.Gates)
		}
		if why.Candidate == nil || why.Candidate.ServerErrors != 0 {
			t.Fatalf("candidate sample = %+v, want no 5xx: only the gate saw this release fail", why.Candidate)
		}
		d.waitDeployment(t, stable.ID, 2*time.Minute, func(dep state.Deployment) bool {
			return dep.Status == state.DeployLive && dep.TrafficPercent == 100
		})
	})

	t.Run("completed release that fails its gate is rolled back and freezes the app", func(t *testing.T) {
		const app = "rg-gate"
		d.waitServing(t, app, 2*time.Minute)
		g.mustStatus(t, d.key, http.MethodPost, "/v1/freezes",
			api.CreateReleaseFreezeRequest{App: app, Kind: api.ReleaseFreezeKindAfterRollback, AfterRollbackMinutes: 30, Reason: "drill after rollback"},
			http.StatusCreated, nil)
		rollback := true
		started := time.Now()
		release := d.deployLive(t, app, api.CreateDeploymentRequest{Image: reject409,
			Canary: &api.CanaryPresetSpec{Preset: "none"}, RollbackOn5xx: &rollback})
		release = d.waitDeployment(t, release.ID, time.Minute, func(dep state.Deployment) bool {
			return dep.TrafficPercent == 100 && dep.RolloutState == "complete"
		})
		stop := d.sendTraffic(app, "/api", 500*time.Millisecond)
		got := d.waitDeployment(t, release.ID, 6*time.Minute, func(dep state.Deployment) bool { return dep.LastAutoRollbackReason != "" })
		stop()
		var why api.RolloutDecision
		if err := json.Unmarshal(got.RolloutDecision, &why); err != nil || why.Rule != api.RolloutRuleAbortPostReleaseGate {
			t.Fatalf("post-release decision = %s (err %v), want %s", got.RolloutDecision, err, api.RolloutRuleAbortPostReleaseGate)
		}
		t.Logf("DRILL post_release_gate seconds=%.0f reason=%q gates=%+v summary=%q",
			time.Since(started).Seconds(), got.LastAutoRollbackReason, why.Gates, why.Summary)
		d.waitServing(t, app, 3*time.Minute)

		// The after_rollback policy froze the app when Gregale rolled back.
		var status api.ReleaseFreezeStatus
		deadline := time.Now().Add(time.Minute)
		for !status.Frozen && time.Now().Before(deadline) {
			g.mustStatus(t, d.key, http.MethodGet, "/v1/apps/"+app+"/freeze-status", nil, http.StatusOK, &status)
			time.Sleep(2 * time.Second)
		}
		if !status.Frozen || !slices.ContainsFunc(status.Active, func(f api.ReleaseFreeze) bool { return f.Source == "automatic_rollback" }) {
			t.Fatalf("freeze status after the rollback = %+v, want an automatic_rollback freeze", status)
		}
		raw, code := doReq(t, h, d.key, http.MethodPost, "/v1/apps/"+app+"/deployments", api.CreateDeploymentRequest{Image: v2})
		if code != http.StatusConflict || !strings.Contains(string(raw), api.CodeReleaseFrozen) {
			t.Fatalf("deploy after the automatic rollback: status=%d body=%s, want 409 %s", code, raw, api.CodeReleaseFrozen)
		}
		t.Logf("DRILL after_rollback_freeze active=%d", len(status.Active))
	})

	t.Run("release waits for another admin's approval; a rejection aborts it", func(t *testing.T) {
		const app = "rg-approve"
		// Org apps answer only to keys bound to their org.
		deployerKey, approverKey := g.orgWithApprover(t, ctx, app)
		od := *d
		od.key = deployerKey
		og := &guardsDrill{d: &od, pool: store}
		od.deployLive(t, app, api.CreateDeploymentRequest{Image: v1})
		od.waitServing(t, app, time.Minute)
		og.mustStatus(t, od.key, http.MethodPut, "/v1/apps/"+app+"/release-approval-policy",
			api.SetReleaseApprovalPolicyRequest{RequiredApprovals: 1, AfterStage: 1, NotifyChannels: []string{channel}}, http.StatusOK, nil)

		candidate := od.deployLive(t, app, api.CreateDeploymentRequest{Image: v2, Canary: customCanary("1", "30s")})
		waiting := og.waitApproval(t, candidate.ID, 3*time.Minute, api.ReleaseApprovalStateWaiting)
		// Well past the 30 s stage, the release must still be held at 1%.
		time.Sleep(45 * time.Second)
		held, _ := store.DeploymentByID(ctx, candidate.ID)
		if held.TrafficPercent != 1 || held.RolloutState == "complete" {
			t.Fatalf("waiting release = traffic:%d rollout:%q, want held at 1%%", held.TrafficPercent, held.RolloutState)
		}
		if rules := od.holdRules(t, candidate.ID); !slices.Contains(rules, api.RolloutRuleHoldAwaitingApproval) {
			t.Fatalf("hold rules = %v, want %s", rules, api.RolloutRuleHoldAwaitingApproval)
		}
		t.Logf("DRILL approval_waiting since=%s expires=%s", waiting.WaitingSince, waiting.ExpiresAt)

		// The deployer's own approval does not count.
		var own api.ReleaseApprovalStatus
		og.mustStatus(t, od.key, http.MethodPost, "/v1/deployments/"+candidate.ID+"/approvals",
			api.DecideReleaseApprovalRequest{Decision: api.ReleaseApprovalDecisionApprove}, http.StatusOK, &own)
		if own.State != api.ReleaseApprovalStateWaiting || own.Approvals != 0 || len(own.Decisions) != 1 || own.Decisions[0].Counted {
			t.Fatalf("self approval = %+v, want recorded, not counted, still waiting", own)
		}
		approvedAt := time.Now()
		og.mustStatus(t, approverKey, http.MethodPost, "/v1/deployments/"+candidate.ID+"/approvals",
			api.DecideReleaseApprovalRequest{Decision: api.ReleaseApprovalDecisionApprove, Comment: "looks good"}, http.StatusOK, nil)
		done := od.waitDeployment(t, candidate.ID, 4*time.Minute, func(dep state.Deployment) bool {
			return dep.RolloutState == "complete" || dep.RolloutState == "aborted"
		})
		if done.RolloutState != "complete" || done.TrafficPercent != 100 {
			t.Fatalf("approved release = state:%q traffic:%d reason:%q, want complete", done.RolloutState, done.TrafficPercent, done.RolloutAbortedReason)
		}
		t.Logf("DRILL approval_to_complete seconds=%.0f", time.Since(approvedAt).Seconds())

		rejected := od.deployLive(t, app, api.CreateDeploymentRequest{Image: v3, Canary: customCanary("1", "30s")})
		og.waitApproval(t, rejected.ID, 3*time.Minute, api.ReleaseApprovalStateWaiting)
		og.mustStatus(t, approverKey, http.MethodPost, "/v1/deployments/"+rejected.ID+"/approvals",
			api.DecideReleaseApprovalRequest{Decision: api.ReleaseApprovalDecisionReject, Comment: "not this week"}, http.StatusOK, nil)
		aborted := od.waitDeployment(t, rejected.ID, 3*time.Minute, func(dep state.Deployment) bool { return dep.RolloutState == "aborted" })
		if why := od.decisionRule(t, aborted); why != api.RolloutRuleAbortApprovalReject {
			t.Fatalf("rejected release rule = %s, want %s", why, api.RolloutRuleAbortApprovalReject)
		}
		od.waitDeployment(t, candidate.ID, 2*time.Minute, func(dep state.Deployment) bool {
			return dep.Status == state.DeployLive && dep.TrafficPercent == 100
		})

		// Requested, approved and rejected notices reached the email channel.
		for _, subject := range []string{"Approval needed: release v2 of " + app, "Approved: release v2 of " + app,
			"Approval needed: release v3 of " + app, "Rejected: release v3 of " + app} {
			og.waitMail(t, subject, 2*time.Minute)
		}
		g.waitChannelDelivered(t, channel, approvedAt.Add(-time.Minute))
	})

	t.Run("scheduled release stays dark until its time; a freeze stops the next one", func(t *testing.T) {
		const app = "rg-sched"
		d.createApp(t, app)
		d.deployLive(t, app, api.CreateDeploymentRequest{Image: v1})
		d.waitServing(t, app, time.Minute)

		at := time.Now().Add(3 * time.Minute).UTC().Truncate(time.Second)
		scheduled := d.deployLive(t, app, api.CreateDeploymentRequest{Image: v2, ReleaseAt: at.Format(time.RFC3339),
			ReleaseNotify: []string{channel}, Canary: &api.CanaryPresetSpec{Preset: "none"}})
		var sched api.ScheduledRelease
		g.mustStatus(t, d.key, http.MethodGet, "/v1/deployments/"+scheduled.ID+"/schedule", nil, http.StatusOK, &sched)
		if sched.State != api.ScheduledReleaseStatePending || scheduled.TrafficPercent != 0 {
			t.Fatalf("scheduled release = traffic:%d schedule:%+v, want dark and scheduled", scheduled.TrafficPercent, sched)
		}
		// Dark until a few seconds before its time.
		for time.Until(at) > 10*time.Second {
			dep, err := store.DeploymentByID(ctx, scheduled.ID)
			if err != nil || dep.TrafficPercent != 0 {
				t.Fatalf("scheduled release took traffic %s early: traffic=%d err=%v", time.Until(at), dep.TrafficPercent, err)
			}
			time.Sleep(5 * time.Second)
		}
		got := d.waitDeployment(t, scheduled.ID, at.Sub(time.Now())+2*time.Minute, func(dep state.Deployment) bool { return dep.TrafficPercent == 100 })
		g.mustStatus(t, d.key, http.MethodGet, "/v1/deployments/"+scheduled.ID+"/schedule", nil, http.StatusOK, &sched)
		startedAt, _ := time.Parse(time.RFC3339Nano, sched.StartedAt)
		t.Logf("DRILL schedule_started state=%s late_seconds=%.0f traffic=%d", sched.State, startedAt.Sub(at).Seconds(), got.TrafficPercent)
		if sched.State != api.ScheduledReleaseStateStarted || startedAt.Before(at) {
			t.Fatalf("schedule after its time = %+v, want started at or after %s", sched, at)
		}
		g.waitMail(t, "Started: scheduled release v2 of "+app, 2*time.Minute)

		// A freeze created after the schedule stops it at its time: the
		// worker rechecks freezes when the release is due.
		at = time.Now().Add(4 * time.Minute).UTC().Truncate(time.Second)
		var accepted api.DeploymentResponse
		g.mustStatus(t, d.key, http.MethodPost, "/v1/apps/"+app+"/deployments", api.CreateDeploymentRequest{Image: v3,
			ReleaseAt: at.Format(time.RFC3339), ReleaseNotify: []string{channel}, Canary: &api.CanaryPresetSpec{Preset: "none"}},
			http.StatusAccepted, &accepted)
		var freeze api.ReleaseFreeze
		g.mustStatus(t, d.key, http.MethodPost, "/v1/freezes",
			api.CreateReleaseFreezeRequest{App: app, Kind: api.ReleaseFreezeKindWindow, Reason: "drill incident"}, http.StatusCreated, &freeze)
		next := d.waitDeployment(t, accepted.ID, 3*time.Minute, func(dep state.Deployment) bool { return dep.Status == state.DeployLive })
		if time.Until(at) <= 0 {
			t.Fatalf("scheduled release %s was not live before its time %s", next.ID, at)
		}
		deadline := at.Add(2 * time.Minute)
		for sched.State != api.ScheduledReleaseStateBlocked && time.Now().Before(deadline) {
			time.Sleep(5 * time.Second)
			g.mustStatus(t, d.key, http.MethodGet, "/v1/deployments/"+next.ID+"/schedule", nil, http.StatusOK, &sched)
		}
		blocked, _ := store.DeploymentByID(ctx, next.ID)
		t.Logf("DRILL schedule_blocked state=%s outcome=%s traffic=%d", sched.State, sched.OutcomeCode, blocked.TrafficPercent)
		if sched.State != api.ScheduledReleaseStateBlocked || sched.OutcomeCode != api.CodeReleaseFrozen || blocked.TrafficPercent != 0 {
			t.Fatalf("frozen schedule = %+v traffic=%d, want blocked by %s and still dark", sched, blocked.TrafficPercent, api.CodeReleaseFrozen)
		}
		g.waitMail(t, "Did not start: scheduled release v3 of "+app, 2*time.Minute)
		g.mustStatus(t, d.key, http.MethodDelete, "/v1/freezes/"+freeze.ID, nil, http.StatusOK, nil)
	})
}

// guardsDeployerEmail is the address SeedAccount gives the drill account.
const guardsDeployerEmail = "e2e+hobby+guards@test.example"

// guardsDrill adds the release-guard calls to the safe-release drill.
type guardsDrill struct {
	d    *safeReleaseDrill
	pool *state.PgStore
}

// mustStatus sends a request and decodes the body into out when the status
// is the expected one.
func (g *guardsDrill) mustStatus(t *testing.T, key, method, path string, body any, want int, out any) {
	t.Helper()
	raw, status := doReq(t, g.d.h, key, method, path, body)
	if status != want {
		t.Fatalf("%s %s: status=%d body=%s, want %d", method, path, status, raw, want)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s %s: decode %T: %v body=%s", method, path, out, err, raw)
		}
	}
}

func (g *guardsDrill) deploymentCount(t *testing.T, appID string) int {
	t.Helper()
	var n int
	if err := g.d.h.Pool.QueryRow(context.Background(), `SELECT count(*) FROM deployments WHERE app_id = $1`, appID).Scan(&n); err != nil {
		t.Fatalf("count deployments: %v", err)
	}
	return n
}

// waitAudit waits for an audit event of kind whose data mentions text.
func (g *guardsDrill) waitAudit(t *testing.T, kind, text string) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		var n int
		err := g.d.h.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM audit_log WHERE kind = $1 AND data::text LIKE '%' || $2 || '%'`, kind, text).Scan(&n)
		if err == nil && n > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %s audit event mentioning %q after 1m (err %v)", kind, text, err)
		}
		time.Sleep(2 * time.Second)
	}
}

// orgWithApprover puts the drill account and a second person in an org,
// creates app there, and returns keys bound to the org for both: an
// account's own keys cannot reach an app it shares through an org.
func (g *guardsDrill) orgWithApprover(t *testing.T, ctx context.Context, app string) (deployerKey, approverKey string) {
	t.Helper()
	h, store := g.d.h, g.pool
	h.SeedAccount(ctx, api.PlanHobby, "guards-approver")
	deployer, err := store.AccountByEmail(ctx, guardsDeployerEmail)
	if err != nil {
		t.Fatal(err)
	}
	approver, err := store.AccountByEmail(ctx, "e2e+hobby+guards-approver@test.example")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{deployer.ID, approver.ID} {
		if err := store.MarkAccountEmailVerified(ctx, id); err != nil {
			t.Fatalf("verify %s: %v", id, err)
		}
	}
	org := h.SeedOrg(ctx, deployer.ID, "rg-org", "Release guards drill", state.OrgRoleOwner)
	if err := store.UpdateOrgPlan(ctx, org.ID, api.PlanHobby); err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, approver.ID, state.OrgRoleAdmin, &deployer.ID); err != nil {
		t.Fatal(err)
	}
	deployerKey, approverKey = g.orgKey(t, ctx, org.ID, deployer.ID), g.orgKey(t, ctx, org.ID, approver.ID)
	falsy := false
	g.mustStatus(t, deployerKey, http.MethodPost, "/v1/orgs/rg-org/apps",
		api.CreateAppRequest{Slug: app, Type: "app", RequireAuthn: &falsy}, http.StatusCreated, nil)
	key := deployerKey
	g.d.t.Cleanup(func() {
		if raw, status := doReq(g.d.t, h, key, http.MethodPost, "/v1/apps/"+app+"/park", nil); status != http.StatusNoContent {
			g.d.t.Errorf("cleanup park %s: status=%d body=%s", app, status, raw)
		}
	})
	return deployerKey, approverKey
}

// orgKey mints an admin key bound to org for account, as `gregale keys
// create --org` does after its step-up.
func (g *guardsDrill) orgKey(t *testing.T, ctx context.Context, orgID, accountID string) string {
	t.Helper()
	plaintext, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.pool.CreateOrgAPIKey(ctx, orgID, accountID, hash, "e2e-org", api.ScopesAdminOnly, nil); err != nil {
		t.Fatalf("org key for %s: %v", accountID, err)
	}
	return plaintext
}

func (g *guardsDrill) waitApproval(t *testing.T, deploymentID string, timeout time.Duration, want string) api.ReleaseApprovalStatus {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var got api.ReleaseApprovalStatus
	for {
		raw, status := doReq(t, g.d.h, g.d.key, http.MethodGet, "/v1/deployments/"+deploymentID+"/approvals", nil)
		if status == http.StatusOK && json.Unmarshal(raw, &got) == nil && got.State == want {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("approval of %s = %+v (status %d) after %s, want %s", deploymentID, got, status, timeout, want)
		}
		time.Sleep(3 * time.Second)
	}
}

// waitMail waits for meterd's log mail transport to record a notice with
// this subject.
func (g *guardsDrill) waitMail(t *testing.T, subject string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !strings.Contains(g.d.h.MeterdLogs(), subject) {
		if time.Now().After(deadline) {
			t.Fatalf("no notice %q in meterd's mail log after %s", subject, timeout)
		}
		time.Sleep(3 * time.Second)
	}
	t.Logf("DRILL notice %q", subject)
}

func (g *guardsDrill) waitChannelDelivered(t *testing.T, name string, after time.Time) {
	t.Helper()
	var channels []api.NotificationChannelResponse
	g.mustStatus(t, g.d.key, http.MethodGet, "/v1/notification-channels", nil, http.StatusOK, &channels)
	for _, c := range channels {
		if c.Name != name {
			continue
		}
		delivered, err := time.Parse(time.RFC3339Nano, c.LastDeliveredAt)
		if err != nil || delivered.Before(after) || c.LastError != "" {
			t.Fatalf("channel %s = %+v, want delivered after %s without an error", name, c, after)
		}
		return
	}
	t.Fatalf("channel %s not listed: %+v", name, channels)
}

func failedGate(results []api.ReleaseGateResult, name string) *api.ReleaseGateResult {
	for i := range results {
		if results[i].Name == name && !results[i].Passed {
			return &results[i]
		}
	}
	return nil
}

// decisionRule is the rule of the deployment's latest rollout decision; an
// approval abort records no samples, so it does not need both.
func (d *safeReleaseDrill) decisionRule(t *testing.T, dep state.Deployment) string {
	t.Helper()
	var out api.RolloutDecision
	if err := json.Unmarshal(dep.RolloutDecision, &out); err != nil {
		t.Fatalf("deployment %s rollout_decision = %s: %v", dep.ID, dep.RolloutDecision, err)
	}
	return out.Rule
}
