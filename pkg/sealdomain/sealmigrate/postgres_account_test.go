package sealmigrate_test

import (
	"context"
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/executionpayload"
	"github.com/onebox-faas/faas/pkg/sealdomain/sealmigrate"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestPostgresMigrateAccount runs the account re-seal against PostgreSQL
// (ADR 20261022111812): a TOTP secret, an inbound webhook secret, a billing
// key, a queued workflow's plan and a PagerDuty acknowledgement secret move
// to the domain alone, the plan under the domain's key id, through each
// table's checks; a second run finds them current.
func TestPostgresMigrateAccount(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	fleet, domain := mustIdentity(t), mustIdentity(t)
	toFleet := []*age.X25519Recipient{fleet.Recipient()}
	suffix := strconv.FormatInt(rand.Int64N(1<<40), 36)
	account, err := store.CreateAccount(ctx, "account-domain-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "account-domain-" + suffix, Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	sealBlob := func(namespace, value string) []byte {
		b, err := secretbox.SealBytesTo(toFleet, namespace, []byte(value), 4096)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	mfa, err := secretbox.SealTo(toFleet, secretbox.Envelope{"MFA_SECRET": "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetMFASecret(ctx, account.ID, mfa, nil); err != nil {
		t.Fatal(err)
	}
	limits, _ := api.LimitsFor(api.PlanPro)
	endpoint, err := store.CreateInboundWebhookEndpointIfUnderQuota(ctx, state.InboundWebhookEndpoint{AppID: app.ID, AccountID: account.ID, Name: "stripe-" + suffix,
		Provider: state.InboundWebhookProviderStripe, TokenHash: []byte(strings.Repeat("h", 32)),
		SigningSecretSealed: sealBlob(api.InboundWebhookSecretNamespace, "whsec_account"), DeliveryPath: "/hooks/stripe", Enabled: true}, limits)
	if err != nil {
		t.Fatal(err)
	}
	billingNS := api.BillingProviderKeyNamespace("stripe", account.ID)
	if _, err := store.PutBillingConnection(ctx, state.BillingConnection{AccountID: account.ID, Provider: "stripe", SealedSecret: sealBlob(billingNS, "sk_test_account"), KeyLast4: "ount", Collection: "none"}); err != nil {
		t.Fatal(err)
	}
	plan, err := executionpayload.SealWorkflowPlanTo(toFleet, []byte(`{"steps":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	workflow, err := store.CreateExecutionWorkflowJob(ctx, state.CreateExecutionWorkflowJobParams{AccountID: account.ID, WorkflowID: "chain-" + suffix,
		PlanID: "0123456789abcdef01234567", StepCount: 1, SealedPlan: plan, PayloadKID: fleet.Recipient().String(), CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := store.CreateAlertRule(ctx, state.AlertRule{AccountID: account.ID, AppID: app.ID, Name: "readiness-" + suffix, Enabled: true,
		Metric: state.AlertMetricErrorRate, Comparison: state.AlertGt, Threshold: 5, WindowSpec: state.AlertWindow5m, CooldownMinutes: 30,
		WebhookURL: "https://example.com/hook", WebhookSecretSealed: sealBlob("alert_rule_secret", "whsec-rule")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO mcp_readiness_pagerduty_acknowledgements
		(rule_id, version, enabled, signing_secret_sealed, pagerduty_version, region, fingerprint, service_id, destination)
		VALUES ($1::uuid, 1, true, $2, 1, 'us', $3, 'PSERVICE1', 'primary')`,
		rule.ID, sealBlob(api.MCPReadinessPagerDutyWebhookSecretNamespace, "pd-ack-secret"), strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}

	report, err := sealmigrate.MigrateAccount(ctx, store, domain, []*age.X25519Identity{fleet})
	if err != nil {
		t.Fatal(err)
	}
	if report.Resealed < 5 {
		t.Fatalf("report = %+v, want all five values re-sealed", report)
	}
	for kind, id := range map[string]string{
		state.AccountSealedMFASecret:      account.ID,
		state.AccountSealedInboundWebhook: endpoint.ID,
		state.AccountSealedBillingKey:     account.ID + "/stripe",
		state.AccountSealedWorkflowPlan:   workflow.ID,
		state.AccountSealedPagerDutyAck:   rule.ID + "/primary",
	} {
		v := accountValue(t, store, kind, id)
		if _, _, err := secretbox.OpenBytes(fleet, v.Sealed); err == nil {
			t.Errorf("%s still opens with the fleet identity", kind)
		}
		if _, err := secretbox.Open(fleet, v.Sealed); err == nil {
			t.Errorf("%s still opens with the fleet identity", kind)
		}
		if kind == state.AccountSealedWorkflowPlan {
			if v.KID != domain.Recipient().String() {
				t.Errorf("plan key id = %q, want the domain recipient", v.KID)
			}
			if _, err := executionpayload.DecodeWorkflowPlan(ctx, []*age.X25519Identity{domain}, v.Sealed, v.KID); err != nil {
				t.Errorf("worker decode of the re-sealed plan: %v", err)
			}
		}
	}
	got, err := store.GetBillingConnection(ctx, account.ID, "stripe")
	if err != nil {
		t.Fatal(err)
	}
	if ns, plain, err := secretbox.OpenBytes(domain, got.SealedSecret); err != nil || ns != billingNS || string(plain) != "sk_test_account" {
		t.Fatalf("billing key domain open = %q, %v", ns, err)
	}

	again, err := sealmigrate.MigrateAccount(ctx, store, domain, []*age.X25519Identity{fleet})
	if err != nil || again.Resealed != 0 {
		t.Fatalf("second run = %+v, %v; want nothing re-sealed", again, err)
	}
}

func accountValue(t *testing.T, store *state.PgStore, kind, id string) state.AccountSealedValue {
	t.Helper()
	after := ""
	for {
		page, err := store.ListAccountSealedValues(context.Background(), kind, after, 500)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range page {
			if v.ID == id {
				return v
			}
			after = v.ID
		}
		if len(page) < 500 {
			t.Fatalf("%s %s not listed", kind, id)
		}
	}
}
