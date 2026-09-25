package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

func memOutboundFixture(t *testing.T) (*MemStore, context.Context, Account, App) {
	t.Helper()
	ctx := context.Background()
	m := NewMemStore()
	account, err := m.CreateAccount(ctx, "outbound-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := m.CreateApp(ctx, App{
		AccountID: account.ID, Slug: "outbound-" + uuid.NewString(), Type: AppTypeApp,
		RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	return m, ctx, account, app
}

func memCustomerOutboundOffer(accountID, name string) OutboundIntegrationOffer {
	return OutboundIntegrationOffer{
		ID: uuid.NewString(), AccountID: accountID, Name: name, Origin: "https://api.example.com",
		AllowedMethods: []string{"GET", "POST"}, AllowedPathPrefixes: []string{"/v1"}, Enabled: true,
		CredentialSource: "customer_sealed", OwnerKind: "customer",
	}
}

func TestOutboundIntegrationValidation(t *testing.T) {
	valid := memCustomerOutboundOffer(uuid.NewString(), "stripe")
	tooMany := api.MaxOutboundRequestsPerDay + 1
	cases := []struct {
		name   string
		mutate func(*OutboundIntegrationOffer)
	}{
		{"bad integration id", func(o *OutboundIntegrationOffer) { o.ID = "not-a-uuid" }},
		{"bad account id", func(o *OutboundIntegrationOffer) { o.AccountID = "not-a-uuid" }},
		{"operator owner", func(o *OutboundIntegrationOffer) { o.OwnerKind = "operator" }},
		{"disabled", func(o *OutboundIntegrationOffer) { o.Enabled = false }},
		{"wrong credential source", func(o *OutboundIntegrationOffer) { o.CredentialSource = "operator_env" }},
		{"already configured", func(o *OutboundIntegrationOffer) { o.CredentialConfigured = true }},
		{"zero daily limit", func(o *OutboundIntegrationOffer) { n := int64(0); o.DailyRequestLimit = &n }},
		{"oversized daily limit", func(o *OutboundIntegrationOffer) { o.DailyRequestLimit = &tooMany }},
		{"uppercase name", func(o *OutboundIntegrationOffer) { o.Name = "Stripe" }},
		{"leading hyphen", func(o *OutboundIntegrationOffer) { o.Name = "-stripe" }},
		{"trailing hyphen", func(o *OutboundIntegrationOffer) { o.Name = "stripe-" }},
		{"name too long", func(o *OutboundIntegrationOffer) { o.Name = string(make([]byte, 64)) }},
		{"http origin", func(o *OutboundIntegrationOffer) { o.Origin = "http://api.example.com" }},
		{"origin userinfo", func(o *OutboundIntegrationOffer) { o.Origin = "https://user@api.example.com" }},
		{"origin query", func(o *OutboundIntegrationOffer) { o.Origin = "https://api.example.com?token=x" }},
		{"origin fragment", func(o *OutboundIntegrationOffer) { o.Origin = "https://api.example.com#fragment" }},
		{"noncanonical origin path", func(o *OutboundIntegrationOffer) { o.Origin = "https://api.example.com/a/../b" }},
		{"unsupported method", func(o *OutboundIntegrationOffer) { o.AllowedMethods = []string{"TRACE"} }},
		{"duplicate method", func(o *OutboundIntegrationOffer) { o.AllowedMethods = []string{"GET", "GET"} }},
		{"noncanonical path", func(o *OutboundIntegrationOffer) { o.AllowedPathPrefixes = []string{"/v1/"} }},
		{"duplicate path", func(o *OutboundIntegrationOffer) { o.AllowedPathPrefixes = []string{"/v1", "/v1"} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			offer := copyOutboundOffer(valid)
			tc.mutate(&offer)
			if err := validateCustomerOutboundIntegration(offer); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("validateCustomerOutboundIntegration() = %v, want ErrInvalidArgument", err)
			}
		})
	}
	if err := validateCustomerOutboundIntegration(valid); err != nil {
		t.Fatalf("valid offer rejected: %v", err)
	}
	if !isOutboundIntegrationName("a0-b") || isOutboundIntegrationName("") || isOutboundIntegrationName("-a") || isOutboundIntegrationName("a-") || isOutboundIntegrationName("A") {
		t.Fatal("integration name grammar accepted or rejected an unexpected value")
	}
}

func TestMemStore_OutboundCustomerIntegrationLifecycle(t *testing.T) {
	m, ctx, account, app := memOutboundFixture(t)
	offer := memCustomerOutboundOffer(account.ID, "stripe")
	limit := int64(1000)
	offer.DailyRequestLimit = &limit
	created, err := m.CreateOutboundIntegration(ctx, offer)
	if err != nil {
		t.Fatalf("CreateOutboundIntegration: %v", err)
	}
	if created.RequestPolicy != api.DefaultOutboundRequestPolicy() {
		t.Fatalf("default request policy = %+v", created.RequestPolicy)
	}
	requestPolicy := api.OutboundRequestPolicy{RatePerSecond: 50, Burst: 250, MaxInFlight: 100, RequestTimeoutMS: 60_000}
	if err := m.SetOutboundRequestPolicy(ctx, account.ID, created.ID, requestPolicy); err != nil {
		t.Fatalf("SetOutboundRequestPolicy: %v", err)
	}
	tooHighPolicy := requestPolicy
	tooHighPolicy.RatePerSecond = 101
	if err := m.SetOutboundRequestPolicy(ctx, account.ID, created.ID, tooHighPolicy); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("over-plan request policy = %v, want ErrInvalidArgument", err)
	}
	if err := m.SetOutboundRequestPolicy(ctx, uuid.NewString(), created.ID, requestPolicy); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account request policy = %v, want ErrNotFound", err)
	}
	updatedOffers, err := m.ListOutboundIntegrationOffers(ctx, account.ID)
	if err != nil || len(updatedOffers) != 1 || updatedOffers[0].RequestPolicy != requestPolicy {
		t.Fatalf("request policy after update = %+v, %v", updatedOffers, err)
	}
	// The store owns copies of all caller-provided slices and pointers.
	offer.AllowedMethods[0] = "DELETE"
	*offer.DailyRequestLimit = 3
	if created.AllowedMethods[0] != "GET" || *created.DailyRequestLimit != 1000 {
		t.Fatalf("created offer aliases input: %+v", created)
	}

	other := memCustomerOutboundOffer(account.ID, "github")
	if _, err := m.CreateOutboundIntegration(ctx, other); err != nil {
		t.Fatalf("create second integration: %v", err)
	}
	operator := OutboundIntegrationOffer{ID: uuid.NewString(), AccountID: account.ID, Name: "internal"}
	m.SeedOutboundIntegrationOffer(operator)
	if got := m.outboundIntegrationOffers[operator.ID]; got.OwnerKind != "operator" || got.CredentialSource != "operator_env" || !got.CredentialConfigured {
		t.Fatalf("Seed defaults = %+v", got)
	}
	offers, err := m.ListOutboundIntegrationOffers(ctx, account.ID)
	if err != nil || len(offers) != 2 || offers[0].Name != "github" || offers[1].Name != "stripe" {
		t.Fatalf("ListOutboundIntegrationOffers = %+v, %v", offers, err)
	}
	if got, err := m.ListOutboundIntegrationOffers(ctx, uuid.NewString()); err != nil || len(got) != 0 {
		t.Fatalf("cross-account list = %+v, %v", got, err)
	}

	if err := m.SetOutboundDailyRequestLimit(ctx, account.ID, created.ID, nil); err != nil {
		t.Fatalf("clear daily limit: %v", err)
	}
	limit = 1000
	if err := m.SetOutboundDailyRequestLimit(ctx, account.ID, created.ID, &limit); err != nil {
		t.Fatalf("set daily limit: %v", err)
	}
	maximum, ok := api.OutboundRequestsPerDayMaxForPlan(account.Plan)
	if !ok {
		t.Fatal("fixture plan has no outbound limit")
	}
	tooHigh := maximum + 1
	if err := m.SetOutboundDailyRequestLimit(ctx, account.ID, created.ID, &tooHigh); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("over-plan daily limit = %v, want ErrInvalidArgument", err)
	}
	if err := m.SetOutboundDailyRequestLimit(ctx, account.ID, operator.ID, &limit); !errors.Is(err, ErrNotFound) {
		t.Fatalf("operator daily limit = %v, want ErrNotFound", err)
	}
	usage, err := m.GetOutboundIntegrationUsage(ctx, account.ID, created.ID)
	if err != nil || usage.DailyRequestCount != 0 || usage.DailyRequestLimit == nil || *usage.DailyRequestLimit != limit || usage.UsageDate == "" || time.Until(usage.ResetsAt) <= 0 {
		t.Fatalf("GetOutboundIntegrationUsage = %+v, %v", usage, err)
	}
	if _, err := m.GetOutboundIntegrationUsage(ctx, uuid.NewString(), created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account usage = %v, want ErrNotFound", err)
	}

	if err := m.SetOutboundCredential(ctx, account.ID, created.ID, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty credential = %v, want ErrNotFound", err)
	}
	if err := m.SetOutboundCredential(ctx, account.ID, created.ID, []byte("sealed")); err != nil {
		t.Fatalf("SetOutboundCredential: %v", err)
	}
	bindings, err := m.BindOutboundIntegration(ctx, account.ID, app.ID, created.ID)
	if err != nil || !bindings.CredentialConfigured || bindings.AppID != app.ID {
		t.Fatalf("BindOutboundIntegration = %+v, %v", bindings, err)
	}
	bindings.RouteMethods[0] = "DELETE"
	bindings, err = m.BindOutboundIntegration(ctx, account.ID, app.ID, created.ID)
	if err != nil || bindings.RouteMethods[0] != "GET" {
		t.Fatalf("idempotent bind leaked a mutable slice: %+v, %v", bindings, err)
	}

	listed, err := m.ListOutboundAppBindings(ctx, account.ID, app.ID)
	if err != nil || len(listed) != 1 || listed[0].CredentialConfigured != true {
		t.Fatalf("ListOutboundAppBindings = %+v, %v", listed, err)
	}
	if err := m.UpdateOutboundBindingPolicy(ctx, account.ID, app.ID, created.ID, []string{"GET"}, []string{"/v1/items"}); err != nil {
		t.Fatalf("UpdateOutboundBindingPolicy valid subset: %v", err)
	}
	if err := m.UpdateOutboundBindingPolicy(ctx, account.ID, app.ID, created.ID, []string{"PATCH"}, []string{"/v1"}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("UpdateOutboundBindingPolicy above ceiling = %v, want ErrInvalidArgument", err)
	}
	if err := m.UpdateOutboundBindingPolicy(ctx, account.ID, uuid.NewString(), created.ID, []string{"GET"}, []string{"/v1"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateOutboundBindingPolicy missing binding = %v, want ErrNotFound", err)
	}

	if err := m.DeleteOutboundCredential(ctx, account.ID, created.ID); err != nil {
		t.Fatalf("DeleteOutboundCredential: %v", err)
	}
	if _, ok := m.outboundCredentials[created.ID]; ok {
		t.Fatal("credential remained after deletion")
	}
	if err := m.UnbindOutboundIntegration(ctx, uuid.NewString(), app.ID, created.ID); err != nil {
		t.Fatalf("cross-account unbind should be idempotent: %v", err)
	}
	if got, err := m.ListOutboundAppBindings(ctx, account.ID, app.ID); err != nil || len(got) != 1 {
		t.Fatalf("cross-account unbind removed binding: %+v, %v", got, err)
	}
	if err := m.UnbindOutboundIntegration(ctx, account.ID, app.ID, created.ID); err != nil {
		t.Fatalf("UnbindOutboundIntegration: %v", err)
	}
	if err := m.UnbindOutboundIntegration(ctx, account.ID, app.ID, created.ID); err != nil {
		t.Fatalf("repeat unbind: %v", err)
	}
	if err := m.DeleteOutboundIntegration(ctx, account.ID, created.ID); err != nil {
		t.Fatalf("DeleteOutboundIntegration: %v", err)
	}
	if err := m.DeleteOutboundIntegration(ctx, account.ID, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("repeat delete = %v, want ErrNotFound", err)
	}
	if err := m.DeleteOutboundIntegration(ctx, account.ID, operator.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("customer delete of operator offer = %v, want ErrNotFound", err)
	}
}

func TestMemStore_OutboundCreateErrorsAndLimit(t *testing.T) {
	m, ctx, account, _ := memOutboundFixture(t)
	valid := memCustomerOutboundOffer(account.ID, "stripe")
	if _, err := m.CreateOutboundIntegration(ctx, valid); err != nil {
		t.Fatal(err)
	}
	duplicateName := memCustomerOutboundOffer(account.ID, "stripe")
	if _, err := m.CreateOutboundIntegration(ctx, duplicateName); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate name = %v, want ErrConflict", err)
	}
	duplicateID := valid
	duplicateID.Name = "stripe-copy"
	if _, err := m.CreateOutboundIntegration(ctx, duplicateID); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate id = %v, want ErrConflict", err)
	}
	missing := memCustomerOutboundOffer(uuid.NewString(), "missing")
	if _, err := m.CreateOutboundIntegration(ctx, missing); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing account = %v, want ErrNotFound", err)
	}
	for i := 1; i < MaxCustomerOutboundIntegrations; i++ {
		offer := memCustomerOutboundOffer(account.ID, "integration-"+uuid.NewString()[:8])
		if _, err := m.CreateOutboundIntegration(ctx, offer); err != nil {
			t.Fatalf("create integration %d: %v", i, err)
		}
	}
	last := memCustomerOutboundOffer(account.ID, "at-limit")
	if _, err := m.CreateOutboundIntegration(ctx, last); !errors.Is(err, ErrOutboundIntegrationLimit) {
		t.Fatalf("integration over quota = %v, want ErrOutboundIntegrationLimit", err)
	}
}

func TestMemStore_OutboundBindingRejectsUnavailableResources(t *testing.T) {
	m, ctx, account, app := memOutboundFixture(t)
	if _, err := m.BindOutboundIntegration(ctx, account.ID, app.ID, uuid.NewString()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown integration = %v, want ErrNotFound", err)
	}
	offer := memCustomerOutboundOffer(account.ID, "disabled")
	offer.Enabled = false
	m.SeedOutboundIntegrationOffer(offer)
	if _, err := m.BindOutboundIntegration(ctx, account.ID, app.ID, offer.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled integration = %v, want ErrNotFound", err)
	}
	if err := m.DeleteOutboundCredential(ctx, account.ID, offer.ID); err != nil {
		t.Fatalf("delete absent credential should be idempotent: %v", err)
	}
	if err := m.SetOutboundCredential(ctx, account.ID, offer.ID, []byte("sealed")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled credential target = %v, want ErrNotFound", err)
	}
	if err := m.SetOutboundDailyRequestLimit(ctx, account.ID, uuid.NewString(), nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing daily-limit target = %v, want ErrNotFound", err)
	}
	if err := m.DeleteOutboundIntegration(ctx, uuid.NewString(), offer.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account delete = %v, want ErrNotFound", err)
	}
}
