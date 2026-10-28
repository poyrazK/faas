package state_test

import (
	"context"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// quotaSubjectKind creates one resource that counts toward a per-account plan
// allowance, on an app or, when the kind has one, account-wide.
type quotaSubjectKind struct {
	name        string
	onApp       func(ctx context.Context, store state.Store, app state.App) error
	accountWide func(ctx context.Context, store state.Store, accountID string) error
	accountCap  func(err error) bool
}

// quotaSubjectLimits allows ten resources per app and one per account.
func quotaSubjectLimits() api.Limits {
	limits := api.MustLimitsFor(api.PlanPro)
	limits.LogDrainPerApp, limits.LogDrainPerAccount = 10, 1
	limits.InboundWebhookPerApp, limits.InboundWebhookPerAccount = 10, 1
	limits.AlertRuleLimitPerApp, limits.AlertRuleLimitPerAccount = 10, 1
	limits.CorsPresetsPerApp, limits.CorsPresetsPerAccount = 10, 1
	limits.WebhookPerApp, limits.WebhookPerAccount = 10, 1
	return limits
}

func quotaSubjectKinds() []quotaSubjectKind {
	limits := quotaSubjectLimits()
	alertRule := func(accountID, appID string) state.AlertRule {
		return state.AlertRule{AccountID: accountID, AppID: appID, Name: "rule-" + uuid.NewString(), Enabled: true,
			Metric: state.AlertMetricErrorRate, Comparison: state.AlertGt, Threshold: 0.5, WindowSpec: state.AlertWindow5m,
			WebhookURL: "https://example.com/hook", WebhookSecretSealed: []byte("sealed"), CooldownMinutes: 30}
	}
	return []quotaSubjectKind{{
		name: "log drains",
		onApp: func(ctx context.Context, store state.Store, app state.App) error {
			_, err := store.CreateAppLogDrainIfUnderQuota(ctx, state.AppLogDrain{AppID: app.ID, AccountID: app.AccountID,
				Kind: state.AppLogDrainKindHTTPJSON, TargetURL: "https://logs.example/" + uuid.NewString()}, limits)
			return err
		},
		accountCap: func(err error) bool {
			var q *state.AppLogDrainQuotaError
			return errors.As(err, &q) && q.Scope == state.AppLogDrainQuotaScopeAccount
		},
	}, {
		name: "realtime endpoints",
		onApp: func(ctx context.Context, store state.Store, app state.App) error {
			_, err := store.(state.ManagedRealtimeEndpointStore).CreateManagedRealtimeEndpointIfUnderQuota(ctx, state.ManagedRealtimeEndpoint{
				AccountID: app.AccountID, AppID: app.ID, CallbackURL: "https://example.com", ConnectPath: "/connect",
				MessagePath: "/message", DisconnectPath: "/disconnect", CallbackAuthTokenSealed: []byte("callback"),
				AuthTokenSealed: []byte("client"), Enabled: true}, 10, 1)
			return err
		},
		accountCap: func(err error) bool {
			var q *state.ManagedRealtimeEndpointQuotaError
			return errors.As(err, &q) && q.Scope == state.ManagedRealtimeEndpointQuotaScopeAccount
		},
	}, {
		name: "inbound webhooks",
		onApp: func(ctx context.Context, store state.Store, app state.App) error {
			token := make([]byte, 32)
			_, _ = rand.Read(token)
			_, err := store.(state.InboundWebhookStore).CreateInboundWebhookEndpointIfUnderQuota(ctx, state.InboundWebhookEndpoint{
				AppID: app.ID, AccountID: app.AccountID, Name: "hook", Provider: state.InboundWebhookProviderStripe, DeliveryPath: "/",
				TokenHash: token, SigningSecretSealed: []byte("sealed"), Enabled: true}, limits)
			return err
		},
		accountCap: func(err error) bool {
			var q *state.InboundWebhookQuotaError
			return errors.As(err, &q) && q.Scope == state.InboundWebhookQuotaScopeAccount
		},
	}, {
		name: "alert rules",
		onApp: func(ctx context.Context, store state.Store, app state.App) error {
			_, err := store.CreateAlertRuleIfUnderQuota(ctx, alertRule(app.AccountID, app.ID), limits)
			return err
		},
		accountWide: func(ctx context.Context, store state.Store, accountID string) error {
			_, err := store.CreateAlertRuleIfUnderQuota(ctx, alertRule(accountID, ""), limits)
			return err
		},
		accountCap: func(err error) bool {
			var q *state.AlertRuleQuotaError
			return errors.As(err, &q) && q.Scope == state.AlertRuleQuotaScopeAccount
		},
	}, {
		name: "cors presets",
		onApp: func(ctx context.Context, store state.Store, app state.App) error {
			_, err := store.CreateCorsPresetIfUnderQuota(ctx, state.CorsPreset{AccountID: app.AccountID, AppID: app.ID,
				Name: "preset-" + uuid.NewString()[:8], AllowOrigins: []string{"*"}}, limits)
			return err
		},
		accountWide: func(ctx context.Context, store state.Store, accountID string) error {
			_, err := store.CreateCorsPresetIfUnderQuota(ctx, state.CorsPreset{AccountID: accountID,
				Name: "preset-" + uuid.NewString()[:8], AllowOrigins: []string{"*"}}, limits)
			return err
		},
		accountCap: func(err error) bool {
			var q *state.CorsPresetQuotaError
			return errors.As(err, &q) && q.Scope == state.CorsPresetQuotaScopeAccount
		},
	}, {
		name: "webhooks",
		onApp: func(ctx context.Context, store state.Store, app state.App) error {
			_, err := store.CreateAppWebhookIfUnderQuota(ctx, state.AppWebhook{AccountID: app.AccountID, AppID: app.ID,
				TargetURL: "https://example.com/hook-" + uuid.NewString(), SecretSealed: []byte("sealed"),
				EventFilter: []string{"cron.fired"}, RetryPolicy: state.AppWebhookRetryDefault, Enabled: true}, limits)
			return err
		},
		accountWide: func(ctx context.Context, store state.Store, accountID string) error {
			_, err := store.CreateAccountReleaseWebhookIfUnderQuota(ctx, state.AppWebhook{
				AccountID: accountID, TargetURL: "https://example.com/release-" + uuid.NewString(), SecretSealed: []byte("sealed"),
				EventFilter: []string{string(state.AppWebhookEventDeploymentLive)}, RetryPolicy: state.AppWebhookRetryDefault, Enabled: true}, limits)
			return err
		},
		accountCap: func(err error) bool {
			var q *state.AppWebhookQuotaError
			return errors.As(err, &q) && q.Scope == state.AppWebhookQuotaScopeAccount
		},
	}}
}

// Per-account plan allowances belong to an app's billing subject
// (ADR-202610171904). A company app keeps its creator's account_id, yet once
// its organization adopted billing its resources count toward the
// organization's allowance, whoever created the app, and never toward the
// creator's personal one. Until then the company keeps its existing policy:
// its creator's company resources share the creator's allowance.
//
// adr: 202610171904
func TestMemPerAccountQuotasCountTheBillingSubject(t *testing.T) {
	for _, kind := range quotaSubjectKinds() {
		for _, adopted := range []bool{true, false} {
			quotaSubjectContract(t, state.NewMemStore(), kind, adopted, nil)
		}
	}
}

// quotaSubjectContract creates one resource on the creator's company app,
// then one for the creator personally and one on another member's app of the
// same company, against an allowance of one per account. adopt, when set,
// adopts the organization's billing for stores that do not adopt a Free
// organization on create.
func quotaSubjectContract(t *testing.T, store state.Store, kind quotaSubjectKind, adopted bool, adopt func(orgID string)) {
	t.Helper()
	ctx, suffix := t.Context(), uuid.NewString()[:8]
	account := func(name string) state.Account {
		t.Helper()
		acct, err := store.CreateAccount(ctx, name+"-"+suffix+"@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		return acct
	}
	creator, member := account("creator"), account("member")
	// A Free organization starts with billing adopted; a paid one awaits
	// adoption (ADR-202610171904).
	plan := api.PlanPro
	if adopted {
		plan = api.PlanFree
	}
	org, err := store.CreateOrg(ctx, state.Org{Slug: "quota-co-" + suffix, Name: "Quota Co", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	if adopted && adopt != nil {
		adopt(org.ID)
	}
	for id, role := range map[string]state.OrgRole{creator.ID: state.OrgRoleOwner, member.ID: state.OrgRoleDeveloper} {
		if err := store.AddOrgMember(ctx, org.ID, id, role, nil); err != nil {
			t.Fatal(err)
		}
	}
	newApp := func(accountID, orgID, slug string) state.App {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{AccountID: accountID, OrgID: orgID, Slug: slug + "-" + suffix, Type: state.AppTypeApp,
			RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	personal, company, members := newApp(creator.ID, "", "personal"), newApp(creator.ID, org.ID, "company"), newApp(member.ID, org.ID, "members")

	if err := kind.onApp(ctx, store, company); err != nil {
		t.Fatalf("%s adopted=%v: first resource on the company app: %v", kind.name, adopted, err)
	}
	// Adopted: the company holds its allowance and the creator's is free.
	// Unadopted: the creator holds theirs and the member's is free.
	creatorsOwn := func() error { return kind.onApp(ctx, store, personal) }
	if kind.accountWide != nil {
		creatorsOwn = func() error { return kind.accountWide(ctx, store, creator.ID) }
	}
	for _, step := range []struct {
		what    string
		create  func() error
		refused bool
	}{
		{"the creator's own", creatorsOwn, !adopted},
		{"one on another member's company app", func() error { return kind.onApp(ctx, store, members) }, adopted},
	} {
		err := step.create()
		if step.refused && !kind.accountCap(err) || !step.refused && err != nil {
			t.Fatalf("%s adopted=%v: %s: %v, want refused=%v", kind.name, adopted, step.what, err, step.refused)
		}
	}
}
