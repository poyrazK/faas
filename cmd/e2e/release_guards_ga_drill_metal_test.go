//go:build metal

// release_guards_ga_drill_metal_test.go — evidence the release guards need
// before GA, beyond TestReleaseGuardsDrillMetal:
//
//   - recovery: an approval wait survives apid and meterd restarts and then
//     times out; a schedule that falls due while apid is down starts when apid
//     returns; a canary keeps being judged by its gates across a meterd restart;
//   - the slo and custom_metric gate kinds (ADR-20261019071734 and its
//     amendment 1: a custom_metric gate reads the metric's history);
//   - an error-rate alert rolling a completed release back (ADR-604, ADR-605);
//   - the critical-route canary guard in abort mode (ADR-458).
//
// Database-restoring rollbacks (ADR-20261023181931) are not drilled here: the
// harness has no managed PostgreSQL provider that restores.
//
// The custom_metric and alert cases need a real Prometheus (the binary on PATH
// or FAAS_E2E_PROMETHEUS_BIN); without one they are skipped and meterd judges
// canaries through the safe-release drill's PromQL stub.
//
// Build tag: metal. Requires /dev/kvm, root, Firecracker, FAAS_TEST_KERNEL.
// Wall clock is about 70 minutes; run with -timeout 150m.

package e2e_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
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

func TestReleaseGuardsGADrillMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping metal release guards GA drill")
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
	t.Setenv("FAAS_E2E_API_HOSTING_SMOKE", "1")

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
	v1Img, _ := e2etest.HelloImageAboveBase("library/ga-v1", safeReleaseDrillBody)
	v1 := registry.AddImage("library/ga-v1", v1Img)
	v2 := image("library/ga-v2")
	reject422 := image("library/ga-reject-422", "-reject-api", "422")
	// Slower than a 25 ms latency SLO, but under the circuit breaker's and
	// the post-release watch's 100 ms latency delta.
	slow := image("library/ga-slow", "-delay-root", "70ms")
	failAPI := image("library/ga-fail-api", "-fail-api")

	operator := freeTCPAddr(t)
	promURL, realProm := startGADrillPrometheus(t)
	var stub *safeReleasePromStub
	if !realProm {
		stub = newSafeReleasePromStub(t)
		promURL = stub.URL()
	}
	h := e2etest.Start(t, pool, e2etest.DeployWake|e2etest.Meterd,
		"FAAS_CANARY_PROGRESSION_TOKEN="+strings.Repeat("c", 40),
		"FAAS_SAFEDEPLOY_TOKEN="+strings.Repeat("s", 40),
		"FAAS_APID_METRICS_ADDR="+operator,
		"FAAS_APID_INTERNAL_BASE_URL=http://"+operator,
		"FAAS_PROMETHEUS_URL="+promURL,
		"FAAS_REQUEST_TELEMETRY_ENABLED=true",
		"FAAS_APP_ERRORS_ENABLED=true",
		"FAAS_APID_APP_ERRORS_SOCKET="+filepath.Join(t.TempDir(), "app_errors.sock"),
		// ADR-745: apid exports pushed custom metrics for Prometheus, the
		// history custom_metric gates read.
		"FAAS_CUSTOM_METRIC_HISTORY_ENABLED=1",
		"FAAS_ALERT_EVAL_INTERVAL=15s",
	)
	targets := []string{h.ScheddMetricsURL, h.GatewayControlURL + "/metrics", "http://" + operator + "/metrics"}
	if realProm {
		configureGADrillPrometheus(t, targets...)
	} else {
		stub.setTargets(targets[:2]...)
	}
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
	// Seven apps: Pro deploys up to 25.
	d := &safeReleaseDrill{t: t, h: h, store: store, key: h.SeedAccount(ctx, api.PlanPro, "ga")}
	d.waitLease(true, 2*time.Minute)
	g := &guardsDrill{d: d, pool: store, plan: api.PlanPro, name: "ga", org: "ga-org"}

	const channel = "ga-mail"
	var mail api.NotificationChannelResponse
	g.mustStatus(t, d.key, http.MethodPost, "/v1/notification-channels",
		api.CreateNotificationChannelRequest{Name: channel, Kind: "email", Email: g.email()}, http.StatusCreated, &mail)

	t.Run("an approval wait survives apid and meterd restarts, then times out", func(t *testing.T) {
		const app = "ga-approve"
		deployerKey, _ := g.orgWithApprover(t, ctx, app)
		openPublicURL(t, d, deployerKey, app)
		od := *d
		od.key = deployerKey
		og := &guardsDrill{d: &od, pool: store, plan: g.plan, name: g.name, org: g.org}
		od.deployLive(t, app, api.CreateDeploymentRequest{Image: v1})
		od.waitServing(t, app, time.Minute)
		og.mustStatus(t, od.key, http.MethodPut, "/v1/apps/"+app+"/release-approval-policy",
			api.SetReleaseApprovalPolicyRequest{RequiredApprovals: 1, AfterStage: 1, TimeoutMinutes: int(api.ReleaseApprovalTimeoutMin / time.Minute),
				NotifyChannels: []string{channel}}, http.StatusOK, nil)

		stop := od.sendTraffic(app, "/", 200*time.Millisecond)
		defer stop()
		candidate := od.deployLive(t, app, api.CreateDeploymentRequest{Image: v2, Canary: customCanary("50", "30s")})
		og.waitApproval(t, candidate.ID, 4*time.Minute, api.ReleaseApprovalStateWaiting)
		waitHoldRule(t, &od, candidate.ID, api.RolloutRuleHoldAwaitingApproval, 4*time.Minute)

		if err := h.RestartAPID(); err != nil {
			t.Fatalf("restart apid: %v", err)
		}
		if err := h.RestartMeterd(); err != nil {
			t.Fatalf("restart meterd: %v", err)
		}
		d.waitLease(true, 3*time.Minute)
		waiting := og.waitApproval(t, candidate.ID, 2*time.Minute, api.ReleaseApprovalStateWaiting)
		held, err := store.DeploymentByID(ctx, candidate.ID)
		if err != nil || held.TrafficPercent != 50 || held.RolloutState == "complete" || held.RolloutState == "aborted" {
			t.Fatalf("waiting release after the restarts = traffic:%d rollout:%q err:%v, want still held at 50%%", held.TrafficPercent, held.RolloutState, err)
		}
		t.Logf("DRILL restart_kept_wait since=%s expires=%s", waiting.WaitingSince, waiting.ExpiresAt)

		expires, _ := time.Parse(time.RFC3339, waiting.ExpiresAt)
		aborted := od.waitDeployment(t, candidate.ID, time.Until(expires)+4*time.Minute, func(dep state.Deployment) bool {
			return dep.RolloutState == "aborted" || dep.RolloutState == "complete"
		})
		if rule := od.decisionRule(t, aborted); aborted.RolloutState != "aborted" || rule != api.RolloutRuleAbortApprovalTimeout {
			t.Fatalf("timed-out release = state:%q rule:%s, want aborted by %s", aborted.RolloutState, rule, api.RolloutRuleAbortApprovalTimeout)
		}
		t.Logf("DRILL approval_timeout late_seconds=%.0f", time.Since(expires).Seconds())
		og.waitMail(t, "Approval timed out: release v2 of "+app, 3*time.Minute)
	})

	t.Run("a schedule that falls due while apid is down starts when apid returns", func(t *testing.T) {
		const app = "ga-sched"
		d.createApp(t, app)
		openPublicURL(t, d, d.key, app)
		d.deployLive(t, app, api.CreateDeploymentRequest{Image: v1})
		d.waitServing(t, app, time.Minute)
		at := time.Now().Add(90 * time.Second).UTC().Truncate(time.Second)
		scheduled := d.deployLive(t, app, api.CreateDeploymentRequest{Image: v2, ReleaseAt: at.Format(time.RFC3339),
			Canary: &api.CanaryPresetSpec{Preset: "none"}})
		var sched api.ScheduledRelease
		g.mustStatus(t, d.key, http.MethodGet, "/v1/deployments/"+scheduled.ID+"/schedule", nil, http.StatusOK, &sched)
		if sched.State != api.ScheduledReleaseStatePending {
			t.Fatalf("schedule = %+v, want pending", sched)
		}
		if err := h.KillAPID(); err != nil {
			t.Fatalf("kill apid: %v", err)
		}
		time.Sleep(time.Until(at.Add(45 * time.Second)))
		restarted := time.Now()
		if err := h.RestartAPID(); err != nil {
			t.Fatalf("restart apid: %v", err)
		}
		d.waitDeployment(t, scheduled.ID, 3*time.Minute, func(dep state.Deployment) bool { return dep.TrafficPercent == 100 })
		g.mustStatus(t, d.key, http.MethodGet, "/v1/deployments/"+scheduled.ID+"/schedule", nil, http.StatusOK, &sched)
		startedAt, _ := time.Parse(time.RFC3339Nano, sched.StartedAt)
		t.Logf("DRILL schedule_after_outage state=%s late_seconds=%.0f after_restart_seconds=%.0f",
			sched.State, startedAt.Sub(at).Seconds(), startedAt.Sub(restarted).Seconds())
		if sched.State != api.ScheduledReleaseStateStarted || startedAt.Before(restarted) {
			t.Fatalf("schedule after the outage = %+v, want started after apid returned at %s", sched, restarted)
		}
	})

	t.Run("a canary keeps being judged by its gates across a meterd restart", func(t *testing.T) {
		const app = "ga-meterd"
		d.createApp(t, app)
		openPublicURL(t, d, d.key, app)
		stable := d.deployLive(t, app, api.CreateDeploymentRequest{Image: v1})
		d.waitServing(t, app, time.Minute)
		zero := 0
		g.mustStatus(t, d.key, http.MethodPost, "/v1/apps/"+app+"/release-gates",
			api.CreateReleaseGateRequest{Name: "no_new_errors", Kind: "new_errors", MaxNewGroups: &zero, MinEvents: 3}, http.StatusCreated, nil)
		stop := d.sendTraffic(app, "/api", 500*time.Millisecond)
		defer stop()
		candidate := d.deployLive(t, app, api.CreateDeploymentRequest{Image: reject422, Canary: customCanary("50", "10m")})
		d.waitDeployment(t, candidate.ID, 3*time.Minute, func(dep state.Deployment) bool { return dep.TrafficPercent == 50 })
		started := time.Now()
		if err := h.RestartMeterd(); err != nil {
			t.Fatalf("restart meterd: %v", err)
		}
		d.waitLease(true, 3*time.Minute)
		got := d.waitDeployment(t, candidate.ID, 8*time.Minute, func(dep state.Deployment) bool {
			return dep.RolloutState == "aborted" || dep.RolloutState == "complete"
		})
		why := d.decision(t, got)
		t.Logf("DRILL gate_after_meterd_restart seconds=%.0f state=%q rule=%s", time.Since(started).Seconds(), got.RolloutState, why.Rule)
		if got.RolloutState != "aborted" || why.Rule != api.RolloutRuleAbortReleaseGate {
			t.Fatalf("canary after the meterd restart = state:%q rule:%s, want aborted by %s", got.RolloutState, why.Rule, api.RolloutRuleAbortReleaseGate)
		}
		d.waitDeployment(t, stable.ID, 2*time.Minute, func(dep state.Deployment) bool { return dep.TrafficPercent == 100 })
	})

	t.Run("an slo gate aborts a canary that burns its latency objective", func(t *testing.T) {
		const app = "ga-slo"
		d.createApp(t, app)
		openPublicURL(t, d, d.key, app)
		stable := d.deployLive(t, app, api.CreateDeploymentRequest{Image: v1})
		d.waitServing(t, app, time.Minute)
		g.mustStatus(t, d.key, http.MethodPost, "/v1/apps/"+app+"/slos",
			api.CreateSLORequest{Name: "fast", SLI: "latency", LatencyThresholdMS: 25, ObjectivePct: 99, WindowDays: 7}, http.StatusCreated, nil)
		g.mustStatus(t, d.key, http.MethodPost, "/v1/apps/"+app+"/release-gates",
			api.CreateReleaseGateRequest{Name: "fast_enough", Kind: "slo", SLO: "fast", MaxBurnRate: 10}, http.StatusCreated, nil)
		stop := d.sendTraffic(app, "/", 100*time.Millisecond)
		defer stop()
		started := time.Now()
		candidate := d.deployLive(t, app, api.CreateDeploymentRequest{Image: slow, Canary: customCanary("50", "10m")})
		got := d.waitDeployment(t, candidate.ID, 8*time.Minute, func(dep state.Deployment) bool {
			return dep.RolloutState == "aborted" || dep.RolloutState == "complete"
		})
		why := d.decision(t, got)
		failed := failedGate(why.Gates, "fast_enough")
		t.Logf("DRILL slo_gate seconds=%.0f state=%q rule=%s gate=%+v candidate_p95=%.0f stable_p95=%.0f",
			time.Since(started).Seconds(), got.RolloutState, why.Rule, failed, why.Candidate.P95LatencyMS, why.Stable.P95LatencyMS)
		if got.RolloutState != "aborted" || why.Rule != api.RolloutRuleAbortReleaseGate || failed == nil {
			t.Fatalf("slow canary = state:%q rule:%s gates:%+v, want aborted by the fast_enough slo gate", got.RolloutState, why.Rule, why.Gates)
		}
		d.waitDeployment(t, stable.ID, 2*time.Minute, func(dep state.Deployment) bool { return dep.TrafficPercent == 100 })
	})

	t.Run("a custom_metric gate judges the history the app pushed", func(t *testing.T) {
		if !realProm {
			t.Skip("no prometheus binary: custom_metric history lives in Prometheus (ADR-745)")
		}
		const app = "ga-metric"
		d.createApp(t, app)
		openPublicURL(t, d, d.key, app)
		stable := d.deployLive(t, app, api.CreateDeploymentRequest{Image: v1})
		d.waitServing(t, app, time.Minute)
		threshold := 5.0
		g.mustStatus(t, d.key, http.MethodPost, "/v1/apps/"+app+"/release-gates",
			api.CreateReleaseGateRequest{Name: "orders", Kind: "custom_metric", Metric: "orders_per_minute", Comparison: "below",
				Threshold: &threshold, MinSamples: 5}, http.StatusCreated, nil)
		stopTraffic := d.sendTraffic(app, "/", 300*time.Millisecond)
		defer stopTraffic()
		// The app reports one order a minute while the candidate serves.
		stopPush := pushCustomMetric(t, d, app, "orders_per_minute", 1, 2*time.Second)
		defer stopPush()
		started := time.Now()
		candidate := d.deployLive(t, app, api.CreateDeploymentRequest{Image: v2, Canary: customCanary("50", "10m")})
		got := d.waitDeployment(t, candidate.ID, 8*time.Minute, func(dep state.Deployment) bool {
			return dep.RolloutState == "aborted" || dep.RolloutState == "complete"
		})
		why := d.decision(t, got)
		failed := failedGate(why.Gates, "orders")
		t.Logf("DRILL custom_metric_gate seconds=%.0f state=%q rule=%s gate=%+v", time.Since(started).Seconds(), got.RolloutState, why.Rule, failed)
		if got.RolloutState != "aborted" || why.Rule != api.RolloutRuleAbortReleaseGate || failed == nil || failed.Samples < 5 || failed.Observed >= threshold {
			t.Fatalf("canary under a falling metric = state:%q rule:%s gates:%+v, want aborted by orders on >= 5 samples below %v",
				got.RolloutState, why.Rule, why.Gates, threshold)
		}
		d.waitDeployment(t, stable.ID, 2*time.Minute, func(dep state.Deployment) bool { return dep.TrafficPercent == 100 })
	})

	t.Run("an error-rate alert rolls a completed release back", func(t *testing.T) {
		if !realProm {
			t.Skip("no prometheus binary: alert rules evaluate PromQL")
		}
		const app = "ga-alert"
		d.createApp(t, app)
		openPublicURL(t, d, d.key, app)
		stable := d.deployLive(t, app, api.CreateDeploymentRequest{Image: v1})
		d.waitServing(t, app, time.Minute)
		action, window, cooldown := "rollback", 900, api.AlertRuleCooldownMinMinutes
		// apid refuses a post-deploy window on a rule that could never roll a
		// completed release back (ADR-605 amendment 1).
		_, status := doReq(t, h, d.key, http.MethodPost, "/v1/apps/"+app+"/alerts", api.CreateAlertRuleRequest{
			Name: "slow", Metric: "latency_p95_ms", Comparison: "gt", Threshold: 40, WindowSpec: "5m",
			Action: &action, PostDeployRollbackWindowSeconds: &window, ChannelIDs: []string{mail.ID}})
		if status != http.StatusBadRequest {
			t.Fatalf("latency rule with a post-deploy window: status=%d, want 400", status)
		}
		g.mustStatus(t, d.key, http.MethodPost, "/v1/apps/"+app+"/alerts", api.CreateAlertRuleRequest{
			Name: "failing", Metric: "error_rate_pct", Comparison: "gt", Threshold: 10, WindowSpec: "5m", CooldownMinutes: &cooldown,
			Action: &action, PostDeployRollbackWindowSeconds: &window, ChannelIDs: []string{mail.ID}}, http.StatusCreated, nil)
		// Healthy traffic first, so the rule's 5m window is still mostly
		// healthy at cutover. The release then fails one request in six and
		// the windowed rate crosses 10% about three minutes later, leaving the
		// fire full minutes of the release's own evidence (ADR-605).
		stopRoot := d.sendTraffic(app, "/", 100*time.Millisecond)
		defer stopRoot()
		stopAPI := d.sendTraffic(app, "/api", 500*time.Millisecond)
		defer stopAPI()
		time.Sleep(3 * time.Minute)
		started := time.Now()
		// Off: the post-release watch (ADR-1445) would roll the release back
		// on its own 5xx regression before the alert fires.
		watch := false
		release := d.deployLive(t, app, api.CreateDeploymentRequest{Image: failAPI, Canary: &api.CanaryPresetSpec{Preset: "none"}, RollbackOn5xx: &watch})
		d.waitDeployment(t, release.ID, 2*time.Minute, func(dep state.Deployment) bool {
			return dep.TrafficPercent == 100 && dep.RolloutState == "complete"
		})
		restored := d.waitDeployment(t, stable.ID, 12*time.Minute, func(dep state.Deployment) bool {
			return dep.Status == state.DeployLive && dep.TrafficPercent == 100
		})
		// The receipt completes once the routing audit lands, just after the
		// predecessor serves again (ADR-604).
		var r api.AlertRollback
		for deadline := time.Now().Add(2 * time.Minute); ; time.Sleep(2 * time.Second) {
			var rollbacks []api.AlertRollback
			g.mustStatus(t, d.key, http.MethodGet, "/v1/apps/"+app+"/alert-rollbacks", nil, http.StatusOK, &rollbacks)
			i := slices.IndexFunc(rollbacks, func(r api.AlertRollback) bool { return r.CandidateDeploymentID == release.ID && r.Historical })
			if i >= 0 && rollbacks[i].Status == "complete" {
				r = rollbacks[i]
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("alert rollbacks = %+v, want a completed historical one for release %s", rollbacks, release.ID)
			}
		}
		t.Logf("DRILL alert_rollback seconds=%.0f status=%s observed=%.1f evidence=%+v restored=%s",
			time.Since(started).Seconds(), r.Status, r.ObservedValue, r.DeploymentEvidence, restored.ID)
		if e := r.DeploymentEvidence; e == nil || e.Status != "breached" || e.ErrorRatePct <= 10 {
			t.Fatalf("historical rollback evidence = %+v, want the release's own error rate breaching 10%%", e)
		}
	})

	t.Run("a critical route regression aborts the canary", func(t *testing.T) {
		const app = "ga-route"
		d.createApp(t, app)
		openPublicURL(t, d, d.key, app)
		stable := d.deployLive(t, app, api.CreateDeploymentRequest{Image: v1})
		d.waitServing(t, app, time.Minute)
		zero := int64(0)
		g.mustStatus(t, d.key, http.MethodPut, "/v1/apps/"+app+"/route-health/gate", api.SetRouteHealthGateRequest{
			Mode: "enforce", OnRegression: "abort", ExpectedRevision: &zero,
			Routes: []api.RouteHealthRoute{{Method: http.MethodGet, Path: "/api"}}}, http.StatusOK, nil)
		// The route guard judges closed one-minute windows that each need 20
		// /api requests on both sides (ADR-454), so /api runs about once a
		// second. Four root loops (about 8 requests a second each) keep /api
		// near 3% of the candidate's traffic, under the circuit breaker's 5%
		// floor, so only the route guard sees the regression. With one root
		// loop the breaker won at 3/45; with /api every 5 s no window settled.
		for range 4 {
			stopRoot := d.sendTraffic(app, "/", 100*time.Millisecond)
			defer stopRoot()
		}
		stopAPI := d.sendTraffic(app, "/api", time.Second)
		defer stopAPI()
		started := time.Now()
		candidate := d.deployLive(t, app, api.CreateDeploymentRequest{Image: failAPI, Canary: customCanary("50", "10m")})
		got := d.waitDeployment(t, candidate.ID, 8*time.Minute, func(dep state.Deployment) bool {
			return dep.RolloutState == "aborted" || dep.RolloutState == "complete"
		})
		// The saved abort lives in the guard's history (ADR-458); the live
		// report re-evaluates current windows, which the abort emptied.
		var history api.RouteHealthHistoryPage
		g.mustStatus(t, d.key, http.MethodGet, "/v1/apps/"+app+"/route-health/deployments/"+candidate.ID+"/history", nil, http.StatusOK, &history)
		i := slices.IndexFunc(history.Entries, func(e api.RouteHealthHistoryEntry) bool { return e.Purpose == "abort" })
		var abort api.RouteHealthHistoryEntry
		if i >= 0 {
			abort = history.Entries[i]
		}
		t.Logf("DRILL route_guard seconds=%.0f state=%q reason=%q abort=%s/%s share=%d->%d", time.Since(started).Seconds(), got.RolloutState,
			got.RolloutAbortedReason, abort.Decision.Status, abort.Decision.Reason, abort.TrafficPercent, abort.RequestedTrafficPercent)
		if got.RolloutState != "aborted" || i < 0 || abort.Decision.Status != "aborted" || abort.RequestedTrafficPercent != 0 {
			t.Fatalf("canary failing /api = state:%q reason:%q history:%+v, want aborted by the route guard", got.RolloutState, got.RolloutAbortedReason, history.Entries)
		}
		d.waitDeployment(t, stable.ID, 2*time.Minute, func(dep state.Deployment) bool { return dep.TrafficPercent == 100 })
	})
}

// waitHoldRule waits until the deployment's audit records hold rule.
func waitHoldRule(t *testing.T, d *safeReleaseDrill, deploymentID, rule string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var rules []string
	for !slices.Contains(rules, rule) {
		if time.Now().After(deadline) {
			t.Fatalf("hold rules of %s = %v after %s, want %s", deploymentID, rules, timeout, rule)
		}
		time.Sleep(5 * time.Second)
		rules = d.holdRules(t, deploymentID)
	}
}

// openPublicURL makes app's public URL answer without a bearer: Pro and Scale
// apps are created with public_auth bearer (ADR-079), which only a PATCH
// changes.
func openPublicURL(t *testing.T, d *safeReleaseDrill, key, app string) {
	t.Helper()
	raw, status := doReq(t, d.h, key, http.MethodPatch, "/v1/apps/"+app,
		map[string]any{"public_auth": map[string]any{"mode": api.AppPublicAuthModeOpen}})
	if status/100 != 2 {
		t.Fatalf("open %s public URL: status=%d body=%s", app, status, raw)
	}
}

// pushCustomMetric pushes value for name every interval, as an app's metrics
// reporter does, until stopped.
func pushCustomMetric(t *testing.T, d *safeReleaseDrill, app, name string, value float64, every time.Duration) (stop func()) {
	t.Helper()
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for {
			if raw, status := doReq(t, d.h, d.key, http.MethodPut, "/v1/apps/"+app+"/custom-metrics/"+name,
				api.CustomMetricRequest{Value: value}); status/100 != 2 {
				t.Errorf("push %s: status=%d body=%s", name, status, raw)
				return
			}
			select {
			case <-done:
				return
			case <-time.After(every):
			}
		}
	}()
	return func() { close(done); <-finished }
}

// gaDrillPrometheus is the drill's real Prometheus: its config file is
// rewritten once the harness's listeners are known.
var gaDrillPrometheus struct {
	configPath, url string
}

// startGADrillPrometheus starts a real Prometheus with no targets yet, so
// meterd can be pointed at it before the harness exists. It reports false
// when no prometheus binary is available.
func startGADrillPrometheus(t *testing.T) (string, bool) {
	t.Helper()
	bin := os.Getenv("FAAS_E2E_PROMETHEUS_BIN")
	if bin == "" {
		var err error
		if bin, err = exec.LookPath("prometheus"); err != nil {
			t.Log("prometheus not found; the custom_metric and alert cases are skipped")
			return "", false
		}
	}
	dir := t.TempDir()
	configPath := filepath.Join(dir, "prometheus.yml")
	if err := os.WriteFile(configPath, []byte("global:\n  scrape_interval: 1s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	addr := freeTCPAddr(t)
	cmd := exec.Command(bin, "--config.file="+configPath, "--storage.tsdb.path="+filepath.Join(dir, "data"),
		"--web.listen-address="+addr, "--web.enable-lifecycle", "--log.level=warn")
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start prometheus: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	promURL := "http://" + addr
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := http.Get(promURL + "/-/ready"); err == nil { //nolint:gosec,noctx // loopback test listener
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				gaDrillPrometheus.configPath, gaDrillPrometheus.url = configPath, promURL
				return promURL, true
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("prometheus at %s not ready after 30s", promURL)
	return "", false
}

// configureGADrillPrometheus points the drill's Prometheus at the harness's
// metrics endpoints, one job each, and reloads it.
func configureGADrillPrometheus(t *testing.T, targets ...string) {
	t.Helper()
	var config strings.Builder
	config.WriteString("global:\n  scrape_interval: 1s\nscrape_configs:\n")
	for i, target := range targets {
		u, err := url.Parse(target)
		if err != nil || u.Host == "" {
			t.Fatalf("metrics target %q: %v", target, err)
		}
		path := u.Path
		if path == "" {
			path = "/metrics"
		}
		fmt.Fprintf(&config, "  - job_name: target-%d\n    metrics_path: %q\n    static_configs:\n      - targets: [%q]\n", i, path, u.Host)
	}
	if err := os.WriteFile(gaDrillPrometheus.configPath, []byte(config.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, gaDrillPrometheus.url+"/-/reload", nil) //nolint:noctx // loopback test listener
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("reload prometheus: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reload prometheus: status %d", resp.StatusCode)
	}
}
