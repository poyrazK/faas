package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/routeprobe"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// healthProbeFake is the route prober and challenge notifier for baseline
// health probes: it answers a probe carrying the deployment's current
// challenge token with status, and an unknown token unattributed.
type healthProbeFake struct {
	noopNotifier
	mu     sync.Mutex
	tokens map[string]string
	status int
	calls  []string
}

func (f *healthProbeFake) Notify(_ context.Context, channel, payload string) error {
	if channel != db.NotifyRouteProbeChallenge {
		return nil
	}
	var p struct {
		DeploymentID string `json:"deployment_id"`
		Token        string `json:"token"`
		Unbilled     bool   `json:"unbilled"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return err
	}
	if !p.Unbilled {
		return errors.New("baseline health probe challenge not marked unbilled")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens[p.DeploymentID] = p.Token
	return nil
}

func (f *healthProbeFake) Configured() bool { return true }

func (f *healthProbeFake) Probe(context.Context, string, string, string, string, string) (routeprobe.Outcome, error) {
	return routeprobe.Unattributed, nil
}

func (f *healthProbeFake) ProbeStatus(_ context.Context, slug, deploymentID, token, method, path string) (int, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, slug+" "+method+" "+path+" "+deploymentID)
	if token == "" || token != f.tokens[deploymentID] {
		return http.StatusServiceUnavailable, false, nil
	}
	return f.status, true, nil
}

// installHealthProbes gives the server a route prober for baseline checks.
func installHealthProbes(t *testing.T, e testEnv) *healthProbeFake {
	t.Helper()
	old := routeProbeChallengeDelay
	routeProbeChallengeDelay = 0
	t.Cleanup(func() { routeProbeChallengeDelay = old })
	f := &healthProbeFake{tokens: map[string]string{}, status: http.StatusOK}
	e.s.notif = f
	e.s.WithRouteProbes(f)
	return f
}

// protectionEnv is an account with a verified email, the protection
// baseline released, a route prober, and one enrolled app.
func protectionEnv(t *testing.T, plan api.Plan, verified bool) (testEnv, state.App) {
	t.Helper()
	e := setup(t, plan)
	installHealthProbes(t, e)
	if verified {
		if err := e.store.MarkAccountEmailVerified(context.Background(), e.acct.ID); err != nil {
			t.Fatal(err)
		}
		acct, err := e.store.AccountByID(context.Background(), e.acct.ID)
		if err != nil {
			t.Fatal(err)
		}
		e.acct = acct
	}
	enableProtectionBaseline(e)
	app := createApp(t, e, "shop")
	return e, app
}

func goLive(t *testing.T, e testEnv, app state.App) {
	t.Helper()
	dep, err := e.store.CreateDeployment(context.Background(), state.Deployment{AppID: app.ID, Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(context.Background(), dep.ID); err != nil {
		t.Fatal(err)
	}
}

// unverifiedAccountStore reports every account's email as unverified
// (MemStore accounts start verified and cannot be unverified).
type unverifiedAccountStore struct{ MemStore *state.MemStore }

func (u *unverifiedAccountStore) AccountByID(ctx context.Context, id string) (state.Account, error) {
	acct, err := u.MemStore.AccountByID(ctx, id)
	acct.EmailVerifiedAt = nil
	return acct, err
}

// reconcileNow makes the app due and runs one sweep.
func reconcileNow(t *testing.T, e testEnv, app state.App) {
	t.Helper()
	reconcileWith(t, e, app, e.store)
}

// reconcileWith runs one sweep against store.
func reconcileWith(t *testing.T, e testEnv, app state.App, store state.Store) {
	t.Helper()
	if err := e.store.FinishAppProtectionReconcile(context.Background(), app.ID, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	saved := e.s.store
	e.s.store = store
	defer func() { e.s.store = saved }()
	if err := e.s.protectionReconcileSweep(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
}

func protectionItems(t *testing.T, e testEnv, slug string) map[string]api.AppProtectionItemStatus {
	t.Helper()
	got := getProtection(t, e, slug)
	out := map[string]api.AppProtectionItemStatus{}
	for _, item := range got.Items {
		out[item.Item] = item
	}
	return out
}

// TestProtectionReconcilerCreatesBaseline pins ADR-20261116120000 §2 and §4:
// nothing is created before the app has a live production deployment; then
// the SLOs, the email channel, both burn alerts and the gate are created
// once, as ordinary objects; a second pass creates no duplicates; an object
// the customer deletes is reported removed and never recreated.
func TestProtectionReconcilerCreatesBaseline(t *testing.T) {
	e, app := protectionEnv(t, api.PlanHobby, true)
	ctx := context.Background()

	reconcileNow(t, e, app)
	for item, status := range protectionItems(t, e, "shop") {
		if status.State != api.AppProtectionItemPending {
			t.Fatalf("before a live deploy: %s = %+v; want pending", item, status)
		}
	}
	p, _, err := e.store.GetAppProtection(ctx, app.ID)
	if err != nil || p.NextReconcileAt.Before(time.Now().Add(api.ProtectionReconcileRetry-time.Minute)) {
		t.Fatalf("not live: next reconcile %v, %v; want a retry", p.NextReconcileAt, err)
	}

	goLive(t, e, app)
	reconcileNow(t, e, app)
	items := protectionItems(t, e, "shop")
	for _, item := range api.ProtectionBaselineItems {
		if items[item.Item].State != api.AppProtectionItemActive || items[item.Item].ObjectID == "" {
			t.Fatalf("%s = %+v; want active", item.Item, items[item.Item])
		}
	}
	if !getProtection(t, e, "shop").Protected {
		t.Fatal("every item active, but not protected")
	}
	check, err := e.store.GetSyntheticCheck(ctx, app.ID, items["health_check"].ObjectID)
	if err != nil || check.Name != "health" || check.Path != app.Manifest.HealthPath || check.Path == "" || check.IntervalSeconds != 3600 || check.Method != http.MethodGet {
		t.Fatalf("health check = %+v, %v", check, err)
	}
	healthRule, err := e.store.AlertRuleByID(ctx, items["health_alert"].ObjectID)
	if err != nil || healthRule.Metric != state.AlertMetricSyntheticConsecutiveFailures || healthRule.SyntheticCheckID != check.ID ||
		healthRule.Threshold != 2 || string(healthRule.Comparison) != "gte" {
		t.Fatalf("health rule = %+v, %v", healthRule, err)
	}

	slos, err := e.store.ListSLOs(ctx, app.ID)
	if err != nil || len(slos) != 2 {
		t.Fatalf("SLOs = %+v, %v; want 2", slos, err)
	}
	for _, slo := range slos {
		switch slo.Name {
		case api.ProtectionBaselineAvailabilitySLOName:
			if slo.SLI != state.SLIAvailability || slo.ObjectiveBP != 9950 || slo.WindowDays != 30 {
				t.Fatalf("availability SLO = %+v", slo)
			}
		case api.ProtectionBaselineLatencySLOName:
			if slo.SLI != state.SLILatency || slo.ObjectiveBP != 9900 || slo.LatencyThresholdMS != 1000 {
				t.Fatalf("latency SLO = %+v", slo)
			}
		default:
			t.Fatalf("unexpected SLO %+v", slo)
		}
	}
	rule, err := e.store.AlertRuleByID(ctx, items["availability_burn_alert"].ObjectID)
	if err != nil || rule.Metric != state.AlertMetricSLOBudgetBurn || rule.Threshold != api.ProtectionBaselineBurnRate ||
		rule.SLOID != items["availability_slo"].ObjectID || !rule.Enabled || rule.WebhookURL != "" {
		t.Fatalf("availability burn rule = %+v, %v", rule, err)
	}
	channels, err := e.store.ListAlertRuleChannels(ctx, rule.ID)
	if err != nil || len(channels) != 1 || channels[0].ID != items["email_channel"].ObjectID || channels[0].Email != e.acct.Email {
		t.Fatalf("rule channels = %+v, %v; want the account email channel", channels, err)
	}
	gates, err := e.store.ListReleaseGates(ctx, app.ID)
	if err != nil || len(gates) != 1 || gates[0].Kind != "slo" || gates[0].SLOID != items["availability_slo"].ObjectID ||
		gates[0].CreatedBy != api.ProtectionBaselineGateActor {
		t.Fatalf("gates = %+v, %v", gates, err)
	}

	reconcileNow(t, e, app)
	if again, _ := e.store.ListSLOs(ctx, app.ID); len(again) != 2 {
		t.Fatalf("second pass: %d SLOs; want no duplicates", len(again))
	}

	if err := e.store.DeleteAlertRule(ctx, rule.ID); err != nil {
		t.Fatal(err)
	}
	reconcileNow(t, e, app)
	items = protectionItems(t, e, "shop")
	if items["availability_burn_alert"].State != api.AppProtectionItemRemoved {
		t.Fatalf("deleted rule = %+v; want removed", items["availability_burn_alert"])
	}
	if _, err := e.store.AlertRuleByID(ctx, rule.ID); err == nil {
		t.Fatal("deleted rule came back")
	}
	rules, err := e.store.ListAlertRulesForAccount(ctx, e.acct.ID)
	if err != nil || len(rules) != 2 {
		t.Fatalf("rules after delete = %+v, %v; want the latency and health rules", rules, err)
	}
	for _, r := range rules {
		if r.Name == api.ProtectionBaselineAvailabilityBurnAlertName {
			t.Fatalf("availability rule recreated: %+v", r)
		}
	}
}

// TestProtectionHealthProbes pins ADR-20261116120000 §3: apid probes the
// baseline check through the route-probe challenge, pinned to the live
// production deployment, records the run in the check's history, and does
// not probe it again within its interval; meterd's runner never sees it;
// an answer the gateway cannot attribute is a failed run.
func TestProtectionHealthProbes(t *testing.T) {
	e, app := protectionEnv(t, api.PlanHobby, true)
	ctx := context.Background()
	fake := e.s.routeProbes.(*healthProbeFake)
	goLive(t, e, app)
	reconcileNow(t, e, app)
	checkID := protectionItems(t, e, "shop")["health_check"].ObjectID

	runnable, err := e.store.ListRunnableSyntheticChecks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range runnable {
		if c.ID == checkID {
			t.Fatal("meterd's runner would probe (and bill) the baseline check")
		}
	}

	if err := e.s.protectionHealthProbeSweep(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	runs, err := e.store.ListSyntheticCheckRuns(ctx, checkID, 10)
	if err != nil || len(runs) != 1 || !runs[0].OK || runs[0].StatusCode != http.StatusOK {
		t.Fatalf("runs = %+v, %v; want one OK run", runs, err)
	}
	live, err := e.store.LiveDeployments(ctx, app.ID)
	if err != nil || len(live) != 1 {
		t.Fatalf("live = %+v, %v", live, err)
	}
	if len(fake.calls) != 1 || fake.calls[0] != "shop GET "+app.Manifest.HealthPath+" "+live[0].ID {
		t.Fatalf("probe calls = %v; want one probe of the health path pinned to the live deployment", fake.calls)
	}
	if err := e.s.protectionHealthProbeSweep(ctx); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("probed again within the interval: %v", fake.calls)
	}

	// A failing app and an unattributed answer both count as failures.
	for _, tc := range []struct {
		name  string
		setup func()
		code  int
	}{
		{"app answers 500", func() { fake.status = http.StatusInternalServerError }, http.StatusInternalServerError},
		{"gateway cannot attribute", func() { fake.status = http.StatusOK; fake.tokens = map[string]string{"other": "x"} }, http.StatusServiceUnavailable},
	} {
		tc.setup()
		probe := protectionHealthTarget{check: state.SyntheticCheck{ID: checkID, Method: http.MethodGet, Path: "/", TimeoutMS: 1000},
			slug: "shop", deploymentID: live[0].ID, token: fake.tokens[live[0].ID]}
		run := probeProtectionHealth(ctx, fake, probe)
		if run.OK || run.ErrorClass != "status" || run.StatusCode != tc.code {
			t.Fatalf("%s: run = %+v; want a failed status run with %d", tc.name, run, tc.code)
		}
	}
}

// TestProtectionHealthCheckNeedsProber: without the route prober a baseline
// check would never run, so it and its alert are skipped.
func TestProtectionHealthCheckNeedsProber(t *testing.T) {
	e, app := protectionEnv(t, api.PlanHobby, true)
	e.s.routeProbes = nil
	goLive(t, e, app)
	reconcileNow(t, e, app)
	items := protectionItems(t, e, "shop")
	if items["health_check"].State != api.AppProtectionItemSkipped || items["health_check"].Reason != api.AppProtectionSkipProbesUnavailable {
		t.Fatalf("health check = %+v; want skipped, probes_unavailable", items["health_check"])
	}
	if items["health_alert"].Reason != api.AppProtectionSkipNeedsCheck || items["availability_slo"].State != api.AppProtectionItemActive {
		t.Fatalf("health alert = %+v, availability SLO = %+v", items["health_alert"], items["availability_slo"])
	}
}

// TestProtectionReconcilerSkipsAndRetries: an unverified email skips the
// channel and the alerts that need it, and they are created once the email
// is verified; a name the customer already uses is never taken over.
func TestProtectionReconcilerSkipsAndRetries(t *testing.T) {
	e, app := protectionEnv(t, api.PlanHobby, true)
	ctx := context.Background()
	if _, err := e.store.CreateSLO(ctx, state.SLO{AccountID: e.acct.ID, AppID: app.ID, Name: api.ProtectionBaselineAvailabilitySLOName,
		SLI: state.SLIAvailability, ObjectiveBP: 9990, WindowDays: 7}, api.MaxSLOsPerApp); err != nil {
		t.Fatal(err)
	}
	goLive(t, e, app)
	reconcileWith(t, e, app, statetest.NewMem(&unverifiedAccountStore{MemStore: e.store}))
	items := protectionItems(t, e, "shop")
	want := map[string][2]string{
		"availability_slo":        {api.AppProtectionItemSkipped, api.AppProtectionSkipNameTaken},
		"latency_slo":             {api.AppProtectionItemActive, ""},
		"email_channel":           {api.AppProtectionItemSkipped, api.AppProtectionSkipEmailUnverified},
		"availability_burn_alert": {api.AppProtectionItemSkipped, api.AppProtectionSkipNeedsSLO},
		"latency_burn_alert":      {api.AppProtectionItemSkipped, api.AppProtectionSkipNeedsChannel},
		"availability_gate":       {api.AppProtectionItemSkipped, api.AppProtectionSkipNeedsSLO},
	}
	for name, w := range want {
		if items[name].State != w[0] || items[name].Reason != w[1] {
			t.Fatalf("%s = %+v; want %s %s", name, items[name], w[0], w[1])
		}
	}
	slos, _ := e.store.ListSLOs(ctx, app.ID)
	for _, slo := range slos {
		if slo.Name == api.ProtectionBaselineAvailabilitySLOName && slo.ObjectiveBP != 9990 {
			t.Fatalf("customer's SLO changed: %+v", slo)
		}
	}

	reconcileNow(t, e, app) // the email is verified now
	items = protectionItems(t, e, "shop")
	if items["email_channel"].State != api.AppProtectionItemActive || items["latency_burn_alert"].State != api.AppProtectionItemActive {
		t.Fatalf("after verifying: channel %+v, latency alert %+v; want both active", items["email_channel"], items["latency_burn_alert"])
	}
	if items["availability_slo"].Reason != api.AppProtectionSkipNameTaken {
		t.Fatalf("availability SLO = %+v; still name_taken", items["availability_slo"])
	}
}

// TestProtectionReconcilerRespectsPlan: Free includes no SLOs or alerts, so
// every item is skipped for the plan and nothing is created.
func TestProtectionReconcilerRespectsPlan(t *testing.T) {
	e, app := protectionEnv(t, api.PlanFree, true)
	goLive(t, e, app)
	reconcileNow(t, e, app)
	items := protectionItems(t, e, "shop")
	for _, name := range []string{"availability_slo", "latency_slo", "email_channel"} {
		if items[name].State != api.AppProtectionItemSkipped || items[name].Reason != api.AppProtectionSkipPlan {
			t.Fatalf("free %s = %+v; want skipped for the plan", name, items[name])
		}
	}
	if slos, _ := e.store.ListSLOs(context.Background(), app.ID); len(slos) != 0 {
		t.Fatalf("free plan got SLOs: %+v", slos)
	}
}

// TestProtectionReconcilerIgnoresInternalApps: an internal app is not
// eligible (ADR-20261116120000 §1).
func TestProtectionReconcilerIgnoresInternalApps(t *testing.T) {
	e, app := protectionEnv(t, api.PlanHobby, true)
	app.Visibility = api.AppVisibilityInternal
	if !protectionEligibleApp(state.App{}) || protectionEligibleApp(app) {
		t.Fatal("eligibility: want a plain app eligible and an internal one not")
	}
	preview := state.App{PreviewOfSlug: "shop"}
	if protectionEligibleApp(preview) {
		t.Fatal("a preview app is eligible")
	}
	_ = e
}
