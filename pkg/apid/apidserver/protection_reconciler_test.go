package apidserver

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// protectionEnv is a Hobby account with a verified email, the protection
// baseline released, and one enrolled app.
func protectionEnv(t *testing.T, plan api.Plan, verified bool) (testEnv, state.App) {
	t.Helper()
	e := setup(t, plan)
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
type unverifiedAccountStore struct{ *state.MemStore }

func (u unverifiedAccountStore) AccountByID(ctx context.Context, id string) (state.Account, error) {
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
	for _, name := range []string{"availability_slo", "latency_slo", "email_channel", "availability_burn_alert", "latency_burn_alert", "availability_gate"} {
		if items[name].State != api.AppProtectionItemActive || items[name].ObjectID == "" {
			t.Fatalf("%s = %+v; want active", name, items[name])
		}
	}
	for _, name := range []string{"health_check", "health_alert"} {
		if items[name].State != api.AppProtectionItemPending {
			t.Fatalf("%s = %+v; want pending until the unbilled probe ships", name, items[name])
		}
	}
	if getProtection(t, e, "shop").Protected {
		t.Fatal("protected with the health check still pending")
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
	if err != nil || len(rules) != 1 || rules[0].Name != api.ProtectionBaselineLatencyBurnAlertName {
		t.Fatalf("rules after delete = %+v, %v; want only the latency rule", rules, err)
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
	reconcileWith(t, e, app, unverifiedAccountStore{e.store})
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
