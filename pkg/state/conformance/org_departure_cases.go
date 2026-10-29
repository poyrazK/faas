package conformance

import (
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// departureFixture is an organization whose company app was created by
// creator, a developer, beside creator's own personal app.
type departureFixture struct {
	org               state.Org
	creator, admin    state.Account
	company, personal state.App
	suffix            string
}

func newDepartureFixture(t *testing.T, fx *Fixture) departureFixture {
	t.Helper()
	s, ctx := fx.Store, fx.Ctx
	d := departureFixture{suffix: uuid.NewString()[:8]}
	var err error
	if d.creator, err = s.CreateAccount(ctx, "departure-creator-"+d.suffix+"@example.com", api.PlanPro); err != nil {
		t.Fatal(err)
	}
	if d.admin, err = s.CreateAccount(ctx, "departure-admin-"+d.suffix+"@example.com", api.PlanPro); err != nil {
		t.Fatal(err)
	}
	if d.org, err = s.CreateOrg(ctx, state.Org{Slug: "departure-" + d.suffix, Name: "Departure", Plan: api.PlanPro}); err != nil {
		t.Fatal(err)
	}
	for member, role := range map[string]state.OrgRole{fx.Account.ID: state.OrgRoleOwner, d.admin.ID: state.OrgRoleAdmin, d.creator.ID: state.OrgRoleDeveloper} {
		if err := s.AddOrgMember(ctx, d.org.ID, member, role, nil); err != nil {
			t.Fatal(err)
		}
	}
	app := func(slug, orgID string) state.App {
		t.Helper()
		created, err := s.CreateApp(ctx, state.App{AccountID: d.creator.ID, OrgID: orgID, Slug: slug + "-" + d.suffix,
			Type: state.AppTypeApp, Runtime: "node22", RAMMB: 128, MaxConcurrency: 1, IdleTimeoutS: 60})
		if err != nil {
			t.Fatalf("create %s: %v", slug, err)
		}
		return created
	}
	d.company, d.personal = app("departure-company", d.org.ID), app("departure-personal", "")
	return d
}

// ADR-20261026151103: an account release webhook fans out a company app's
// deployments only while its account is a member of the company; the
// account's personal apps keep reaching it.
func testAccountReleaseWebhookFollowsOrgAccess(t *testing.T, fx *Fixture) {
	s, ctx := fx.Store, fx.Ctx
	d := newDepartureFixture(t, fx)
	hook, err := s.CreateAccountReleaseWebhookIfUnderQuota(ctx, state.AppWebhook{AccountID: d.creator.ID, Scope: state.AppWebhookScopeAccount,
		TargetURL: "https://example.com/releases-" + d.suffix, SecretSealed: []byte("sealed"),
		EventFilter: []string{string(state.AppWebhookEventDeploymentFailed)}, Enabled: true}, api.Limits{WebhookPerAccount: 5})
	if err != nil {
		t.Fatal(err)
	}
	fail := func(app state.App) {
		t.Helper()
		dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + uuid.NewString(), Status: state.DeployPending})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.UpdateDeploymentStatus(ctx, dep.ID, state.DeployFailed, "failed"); err != nil {
			t.Fatal(err)
		}
	}
	sources := func() map[string]int {
		t.Helper()
		rows, _, err := s.ListAccountReleaseWebhookDeliveries(ctx, d.creator.ID, hook.ID, 50, "")
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]int{}
		for _, row := range rows {
			out[row.AppID]++
		}
		return out
	}
	fail(d.company)
	fail(d.personal)
	if got := sources(); got[d.company.ID] != 1 || got[d.personal.ID] != 1 {
		t.Fatalf("a member's deliveries = %v, want one per app", got)
	}
	if err := s.RemoveOrgMember(ctx, d.org.ID, d.creator.ID); err != nil {
		t.Fatal(err)
	}
	fail(d.company)
	fail(d.personal)
	if got := sources(); got[d.company.ID] != 1 || got[d.personal.ID] != 2 {
		t.Fatalf("a departed member's deliveries = %v, want no new company app delivery", got)
	}
}

// ADR-20261026151103: a departure pauses the integrations the departing
// member configured on the company's apps: webhook alert rules, log drains
// and app webhooks stop; a rule that also acts keeps acting without the
// member's webhook. Another member's integration, and the member's own
// personal app, are left alone.
func testDepartedMemberIntegrationsPause(t *testing.T, fx *Fixture) {
	s, ctx := fx.Store, fx.Ctx
	d := newDepartureFixture(t, fx)
	rule := func(app state.App, action state.AlertAction) state.AlertRule {
		t.Helper()
		created, err := s.CreateAlertRule(ctx, state.AlertRule{AccountID: d.creator.ID, AppID: app.ID, Name: string(action) + "-" + app.Slug,
			Enabled: true, Metric: state.AlertMetricErrorRate, Comparison: state.AlertGt, Threshold: 1, WindowSpec: state.AlertWindow5m,
			Action: action, WebhookURL: "https://creator.example.com/alerts", WebhookSecretSealed: []byte("sealed"), CooldownMinutes: 15})
		if err != nil {
			t.Fatalf("create %s rule: %v", action, err)
		}
		return created
	}
	hook := func(app state.App, author string) state.AppWebhook {
		t.Helper()
		created, err := s.CreateAppWebhook(ctx, state.AppWebhook{AppID: app.ID, AccountID: d.creator.ID, CreatedByAccountID: author,
			TargetURL: "https://hooks.example.com/" + uuid.NewString(), SecretSealed: []byte("sealed"),
			EventFilter: []string{string(state.AppWebhookEventDeploymentLive)}, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	alert, rollback, personalAlert := rule(d.company, state.AlertActionWebhook), rule(d.company, state.AlertActionRollback), rule(d.personal, state.AlertActionWebhook)
	drain, err := s.CreateAppLogDrain(ctx, state.AppLogDrain{AppID: d.company.ID, AccountID: d.creator.ID, Kind: "http_json",
		TargetURL: "https://logs.example.com/" + d.suffix, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	creatorHook, adminHook := hook(d.company, ""), hook(d.company, d.admin.ID)

	if err := s.RemoveOrgMember(ctx, d.org.ID, d.creator.ID); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name        string
		id          string
		wantEnabled bool
		wantHook    bool
	}{
		{"webhook alert", alert.ID, false, true},
		{"rollback alert", rollback.ID, true, false},
		{"personal app alert", personalAlert.ID, true, true},
	} {
		got, err := s.AlertRuleByID(ctx, c.id)
		if err != nil || got.Enabled != c.wantEnabled || (got.WebhookURL != "") != c.wantHook {
			t.Errorf("%s = enabled %v webhook %q (%v), want enabled %v, webhook kept %v", c.name, got.Enabled, got.WebhookURL, err, c.wantEnabled, c.wantHook)
		}
	}
	if got, err := s.AppLogDrainByID(ctx, drain.ID); err != nil || got.Enabled {
		t.Errorf("company drain enabled = %v (%v), want paused", got.Enabled, err)
	}
	if got, err := s.AppWebhookByID(ctx, creatorHook.ID); err != nil || got.Enabled {
		t.Errorf("creator's company webhook enabled = %v (%v), want paused", got.Enabled, err)
	}
	if got, err := s.AppWebhookByID(ctx, adminHook.ID); err != nil || !got.Enabled {
		t.Errorf("admin's company webhook enabled = %v (%v), want kept", got.Enabled, err)
	}
}
