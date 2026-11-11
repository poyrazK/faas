package state_test

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"strings"
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

// errReservedAppCap stands in for apid's plan_eviction_priority_reserved_quota.
var errReservedAppCap = errors.New("reserved app cap reached")

// quotaSubjectLimits allows ten resources per app and one per account.
func quotaSubjectLimits() api.Limits {
	limits := api.MustLimitsFor(api.PlanPro)
	limits.LogDrainPerApp, limits.LogDrainPerAccount = 10, 1
	limits.InboundWebhookPerApp, limits.InboundWebhookPerAccount = 10, 1
	limits.AlertRuleLimitPerApp, limits.AlertRuleLimitPerAccount = 10, 1
	limits.CorsPresetsPerApp, limits.CorsPresetsPerAccount = 10, 1
	limits.WebhookPerApp, limits.WebhookPerAccount = 10, 1
	limits.CronLimitPerApp, limits.CronLimitPerAccount = 10, 1
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
	}, {
		// Reserving an app is apid's PATCH admission (updateApp): count the
		// billing subject's reserved apps, then flip the app.
		name: "reserved apps",
		onApp: func(ctx context.Context, store state.Store, app state.App) error {
			reserved := string(api.EvictionPriorityReserved)
			n, err := store.CountOtherBillingSubjectAppsWithEvictionPriority(ctx, app.ID, reserved)
			if err != nil {
				return err
			}
			if n+1 > 1 {
				return errReservedAppCap
			}
			_, err = store.UpdateApp(ctx, app.ID, state.UpdateAppParams{EvictionPriority: &reserved, SetEvictionPriority: true})
			return err
		},
		accountCap: func(err error) bool { return errors.Is(err, errReservedAppCap) },
	}, {
		name: "crons",
		onApp: func(ctx context.Context, store state.Store, app state.App) error {
			_, err := store.CreateCronIfUnderQuota(ctx, app.ID, "*/5 * * * *", "/tick-"+uuid.NewString()[:8], true, limits)
			return err
		},
		accountCap: cronAccountCap,
	}, {
		// A deploy replaces the app's HTTP crons with its manifest's.
		name: "deployment crons",
		onApp: func(ctx context.Context, store state.Store, app state.App) error {
			staging := store.(state.DeploymentCronStagingStore)
			snapshot, err := staging.DeploymentCronStagingSnapshot(ctx, app.AccountID, app.ID)
			if err != nil {
				return err
			}
			desired := append(slices.Clone(snapshot.Crons), api.DeploymentCronConfig{ID: uuid.NewString(), Schedule: "*/5 * * * *", Path: "/deploy", Enabled: true, Timezone: "UTC"})
			_, err = staging.ApplyDeploymentCronStaging(ctx, app.ID, state.DeploymentCronStagingParams{AccountID: app.AccountID, Limits: limits,
				Request: api.DeploymentCronStagingRequest{SessionID: strings.ReplaceAll(uuid.NewString(), "-", ""), Operation: "stage", ExpectedAppID: app.ID,
					ExpectedDeploymentID: snapshot.LatestDeploymentID, ExpectedRevision: snapshot.Revision, Before: snapshot.Crons, Desired: desired}})
			return err
		},
		accountCap: cronAccountCap,
	}, {
		// A project apply in the app's namespace and organization whose
		// cron runs on the app.
		name: "project crons",
		onApp: func(ctx context.Context, store state.Store, app state.App) error {
			_, _, _, err := store.ApplyProjectPlan(ctx, state.Project{AccountID: app.AccountID, OrgID: app.OrgID, Slug: "crons-" + uuid.NewString()[:8],
				ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose},
				nil, []state.Cron{{AppID: app.ID, Schedule: "*/5 * * * *", Path: "/project", Enabled: true}}, limits)
			return err
		},
		accountCap: func(err error) bool {
			var q *state.QuotaError
			return errors.As(err, &q) && q.Kind == state.QuotaErrorKindCrons && !q.NotAllowed
		},
	}, {
		name: "consumer keys",
		onApp: func(ctx context.Context, store state.Store, app state.App) error {
			consumer, err := store.CreateAPIConsumer(ctx, app.AccountID, app.ID, "ref-"+uuid.NewString()[:8], "quota")
			if err != nil {
				return err
			}
			_, prefix, hash, err := api.GenerateConsumerKey()
			if err != nil {
				return err
			}
			_, err = store.CreateConsumerKeyForConsumerIfUnderQuota(ctx, app.AccountID, consumer.ID, "key", prefix, hash, []string{"read"}, nil,
				state.ConsumerKeyQuota{PerApp: 10, PerAccount: 1})
			return err
		},
		accountCap: func(err error) bool {
			var q *state.ConsumerKeyQuotaError
			return errors.As(err, &q) && q.Scope == api.PlanQuotaScopeAccount
		},
	}, {
		name: "openapi docs",
		onApp: func(ctx context.Context, store state.Store, app state.App) error {
			d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Scope: "production", ImageDigest: "sha256:test"})
			if err != nil {
				return err
			}
			return store.UpsertDeploymentOpenAPIDocIfUnderQuota(ctx, d.ID, app.AccountID, app.ID, []byte(`{"openapi":"3.1.0"}`),
				state.OpenAPIDocSourceManualUpload, false, 1)
		},
		accountCap: func(err error) bool {
			var q *state.OpenAPIDocQuotaError
			return errors.As(err, &q)
		},
	}, {
		name: "openapi imports",
		onApp: func(ctx context.Context, store state.Store, app state.App) error {
			return store.UpsertAppOpenAPIDocIfUnderQuota(ctx, app.ID, app.AccountID, []byte(`{"openapi":"3.1.0","info":{"title":"quota"}}`), 1, "3.1.0", 1)
		},
		accountCap: openAPIImportAccountCap,
	}, {
		// A domain claim spends the pending-domain budget until it is
		// verified (api.CustomDomainLimitsFor).
		name: "pending custom domains",
		onApp: func(ctx context.Context, store state.Store, app state.App) error {
			_, err := store.(pendingDomainClaimer).CreateCustomDomainIfUnderQuota(ctx, "claim-"+uuid.NewString()[:8]+".example.test",
				app.ID, "token-"+uuid.NewString(), 10, 1)
			return err
		},
		accountCap: pendingDomainAccountCap,
	}}
}

// pendingDomainClaimer is the domain claim both stores implement; it is not
// part of state.Store.
type pendingDomainClaimer interface {
	CreateCustomDomainIfUnderQuota(ctx context.Context, domain, appID, token string, appLimit, accountLimit int) (state.CustomDomain, error)
}

func pendingDomainAccountCap(err error) bool {
	var q *state.CustomDomainQuotaError
	return errors.As(err, &q) && q.Scope == "account"
}

func openAPIImportAccountCap(err error) bool {
	var q *state.QuotaError
	return errors.As(err, &q) && q.Kind == state.QuotaErrorKindOpenAPIImports && !q.NotAllowed
}

func cronAccountCap(err error) bool {
	var q *state.CronQuotaError
	return errors.As(err, &q) && q.Scope == state.CronQuotaScopeAccount
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
			t.Run(fmt.Sprintf("%s/adopted=%v", kind.name, adopted), func(t *testing.T) {
				quotaSubjectContract(t, state.NewMemStore(), kind, adopted, nil)
			})
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
	if adopted {
		if adopt != nil {
			adopt(org.ID)
		}
		// A company keeps its adoption when it upgrades, and its plan's
		// application quota then admits the contract's company apps where a
		// project apply checks it.
		if err := store.UpdateOrgPlan(ctx, org.ID, api.PlanPro); err != nil {
			t.Fatal(err)
		}
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
