// adr: 20261019180000
package conformance

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// enrollRehearsalApp creates a fresh preview app in the fixture account and
// enrolls it in a capture rehearsal run.
func enrollRehearsalApp(t *testing.T, fx *Fixture) state.App {
	t.Helper()
	expires := time.Now().Add(time.Hour)
	app, err := fx.Store.CreateApp(fx.Ctx, state.App{AccountID: fx.Account.ID, Slug: "rehearsal-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128,
		PreviewOfSlug: fx.App.Slug, PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expires})
	if err != nil {
		t.Fatal(err)
	}
	run := strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := fx.Store.RegisterScenarioTestMembers(fx.Ctx, fx.Account.ID, run, []state.ScenarioTestMember{{AppID: app.ID, Workload: "primary", WebhookMode: state.ScenarioWebhookCaptureMode}}); err != nil {
		t.Fatalf("enroll rehearsal app: %v", err)
	}
	return app
}

// testRehearsalAppLogsStayInsideTheRun pins that no log drain worker starts
// for a rehearsal-enrolled app, while an ordinary app's drain still does.
func testRehearsalAppLogsStayInsideTheRun(t *testing.T, fx *Fixture) {
	t.Helper()
	enrolled := enrollRehearsalApp(t, fx)
	ids := map[string]string{}
	for _, app := range []string{fx.App.ID, enrolled.ID} {
		drain, err := fx.Store.CreateAppLogDrain(fx.Ctx, state.AppLogDrain{AppID: app, AccountID: fx.Account.ID, Kind: state.AppLogDrainKindHTTPJSON,
			TargetURL: "https://logs.example/" + app, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		ids[app] = drain.ID
	}
	drains, err := fx.Store.ListEnabledAppLogDrains(fx.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, d := range drains {
		seen[d.ID] = true
	}
	if !seen[ids[fx.App.ID]] || seen[ids[enrolled.ID]] {
		t.Fatalf("enabled drains: ordinary=%v rehearsal=%v; want true,false", seen[ids[fx.App.ID]], seen[ids[enrolled.ID]])
	}
}

// testRehearsalAppsNeverPollRealBrokers pins that schedd lists no external
// broker trigger for a rehearsal-enrolled app (it would consume or commit
// offsets production consumers own), while an ordinary app's broker trigger is
// still listed.
func testRehearsalAppsNeverPollRealBrokers(t *testing.T, fx *Fixture) {
	t.Helper()
	if err := fx.Store.UpdateAccountPlan(fx.Ctx, fx.Account.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	limits, _ := api.LimitsFor(api.PlanPro)
	enrolled := enrollRehearsalApp(t, fx)
	kafka := []byte(`{"brokers":["b:9092"],"topic":"orders","group":"g"}`)
	create := func(app, kind string, config []byte) string {
		t.Helper()
		trigger, err := fx.Store.CreateTriggerIfUnderQuota(fx.Ctx, app, kind, kind+"-"+uuid.NewString()[:8], true, config, "", 10, 1000, 3, 6*1024*1024, api.BrokerPoisonStrategyCommit, limits)
		if err != nil {
			t.Fatalf("create %s trigger: %v", kind, err)
		}
		return trigger.ID.String()
	}
	ordinaryBroker := create(fx.App.ID, string(api.TriggerKindKafka), kafka)
	rehearsalBroker := create(enrolled.ID, string(api.TriggerKindKafka), kafka)
	triggers, err := fx.Store.ListEnabledTriggers(fx.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, trigger := range triggers {
		listed[trigger.ID.String()] = true
	}
	if !listed[ordinaryBroker] || listed[rehearsalBroker] {
		t.Fatalf("listed broker triggers: ordinary=%v rehearsal=%v; want true,false", listed[ordinaryBroker], listed[rehearsalBroker])
	}
}

// testRehearsalCronsNeverFireFromTheClock pins that schedd's cron listing
// omits a rehearsal-enrolled app's crons, so its recipe alone fires them and
// both revisions see the same number of fires (ADR-20261020200000).
func testRehearsalCronsNeverFireFromTheClock(t *testing.T, fx *Fixture) {
	t.Helper()
	enrolled := enrollRehearsalApp(t, fx)
	ordinary, err := fx.Store.CreateCron(fx.Ctx, fx.App.ID, "*/5 * * * *", "/tick", true)
	if err != nil {
		t.Fatal(err)
	}
	rehearsal, err := fx.Store.CreateCron(fx.Ctx, enrolled.ID, "*/5 * * * *", "/tick", true)
	if err != nil {
		t.Fatal(err)
	}
	crons, err := fx.Store.ListEnabledCrons(fx.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, c := range crons {
		listed[c.ID] = true
	}
	if !listed[ordinary.ID] || listed[rehearsal.ID] {
		t.Fatalf("clock-fired crons: ordinary=%v rehearsal=%v; want true,false", listed[ordinary.ID], listed[rehearsal.ID])
	}
}

// testRehearsalAppsLeaveAccountAlerts pins that account-wide alert aggregates
// skip rehearsal-enrolled apps, whose candidate revisions and injected faults
// must not page the account, while app-scoped rules still count them.
func testRehearsalAppsLeaveAccountAlerts(t *testing.T, fx *Fixture) {
	t.Helper()
	since := time.Now().Add(-time.Hour)
	enrolled := enrollRehearsalApp(t, fx)
	baseDeployments, err := fx.Store.CountFailedDeploymentsSince(fx.Ctx, fx.Account.ID, "", since)
	if err != nil {
		t.Fatal(err)
	}
	baseInvocations, err := fx.Store.CountFailedInvocationsSince(fx.Ctx, fx.Account.ID, "", "", since)
	if err != nil {
		t.Fatal(err)
	}
	for _, app := range []string{fx.App.ID, enrolled.ID} {
		dep, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{AppID: app, Kind: state.DeploymentKindImage, ImageDigest: "sha256:alert-" + app, Status: state.DeployPending})
		if err != nil {
			t.Fatal(err)
		}
		if err := fx.Store.UpdateDeploymentStatus(fx.Ctx, dep.ID, state.DeployFailed, "candidate failed"); err != nil {
			t.Fatal(err)
		}
		inv, err := fx.Store.EnqueueInvocation(fx.Ctx, state.Invocation{AppID: app, AccountID: fx.Account.ID, Source: state.InvocationAsyncInvoke,
			Method: "POST", Path: "/work", Payload: []byte(`{}`), DueAt: time.Now().Add(-time.Second)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fx.Store.ClaimInvocationWithCap(fx.Ctx, inv.ID, "", 60, 10); err != nil {
			t.Fatal(err)
		}
		if err := fx.Store.FailInvocation(fx.Ctx, inv.ID, "exhausted", time.Nanosecond, 1); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name   string
		scope  string
		wantDp int
		wantIv int
	}{
		{"account-wide skips the rehearsal app", "", baseDeployments + 1, baseInvocations + 1},
		{"app-scoped rule on the rehearsal app still counts it", enrolled.ID, 1, 1},
	} {
		deployments, err := fx.Store.CountFailedDeploymentsSince(fx.Ctx, fx.Account.ID, tc.scope, since)
		if err != nil || deployments != tc.wantDp {
			t.Errorf("%s: failed deployments = %d, %v; want %d", tc.name, deployments, err, tc.wantDp)
		}
		invocations, err := fx.Store.CountFailedInvocationsSince(fx.Ctx, fx.Account.ID, tc.scope, "", since)
		if err != nil || invocations != tc.wantIv {
			t.Errorf("%s: failed invocations = %d, %v; want %d", tc.name, invocations, err, tc.wantIv)
		}
	}
}
