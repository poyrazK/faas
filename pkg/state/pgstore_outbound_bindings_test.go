package state_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func pgCustomerOutboundOffer(accountID, name string) state.OutboundIntegrationOffer {
	return state.OutboundIntegrationOffer{
		ID: uuid.NewString(), AccountID: accountID, Name: name, Origin: "https://api.example.com",
		AllowedMethods: []string{"GET", "POST"}, AllowedPathPrefixes: []string{"/v1"}, Enabled: true,
		CredentialSource: "customer_sealed", OwnerKind: "customer",
	}
}

func TestPgStore_OutboundBindingCustomerLifecycle(t *testing.T) {
	s, ctx := pgStore(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx)
	offer := pgCustomerOutboundOffer(accountID, "stripe")
	limit := int64(1000)
	offer.DailyRequestLimit = &limit
	created, err := s.CreateOutboundIntegration(ctx, offer)
	if err != nil || created.ID != offer.ID {
		t.Fatalf("CreateOutboundIntegration = %+v, %v", created, err)
	}

	listed, err := s.ListOutboundIntegrationOffers(ctx, accountID)
	if err != nil || len(listed) != 1 || listed[0].ID != offer.ID || listed[0].DailyRequestLimit == nil || *listed[0].DailyRequestLimit != limit {
		t.Fatalf("ListOutboundIntegrationOffers = %+v, %v", listed, err)
	}
	if cross, err := s.ListOutboundIntegrationOffers(ctx, uuid.NewString()); err != nil || len(cross) != 0 {
		t.Fatalf("cross-account offers = %+v, %v", cross, err)
	}

	usage, err := s.GetOutboundIntegrationUsage(ctx, accountID, offer.ID)
	if err != nil || usage.DailyRequestCount != 0 || usage.DailyRequestLimit == nil || *usage.DailyRequestLimit != limit || usage.UsageDate == "" || usage.ResetsAt.IsZero() {
		t.Fatalf("initial usage = %+v, %v", usage, err)
	}
	if err := s.SetOutboundDailyRequestLimit(ctx, accountID, offer.ID, nil); err != nil {
		t.Fatalf("clear daily limit: %v", err)
	}
	if err := s.SetOutboundDailyRequestLimit(ctx, accountID, offer.ID, &limit); err != nil {
		t.Fatalf("set daily limit: %v", err)
	}
	maxForPlan, ok := api.OutboundRequestsPerDayMaxForPlan(api.PlanPro)
	if !ok {
		t.Fatal("Pro plan has no outbound daily limit")
	}
	tooHighForPlan := maxForPlan + 1
	if err := s.SetOutboundDailyRequestLimit(ctx, accountID, offer.ID, &tooHighForPlan); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("over-plan daily limit = %v, want ErrInvalidArgument", err)
	}
	tooHighGlobally := api.MaxOutboundRequestsPerDay + 1
	if err := s.SetOutboundDailyRequestLimit(ctx, accountID, offer.ID, &tooHighGlobally); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("over-global daily limit = %v, want ErrInvalidArgument", err)
	}
	if err := s.SetOutboundDailyRequestLimit(ctx, uuid.NewString(), offer.ID, nil); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account daily limit = %v, want ErrNotFound", err)
	}

	if err := s.SetOutboundCredential(ctx, accountID, offer.ID, nil); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("empty credential = %v, want ErrNotFound", err)
	}
	if err := s.SetOutboundCredential(ctx, uuid.NewString(), offer.ID, []byte("sealed")); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account credential = %v, want ErrNotFound", err)
	}
	if err := s.SetOutboundCredential(ctx, accountID, offer.ID, []byte("sealed")); err != nil {
		t.Fatalf("SetOutboundCredential: %v", err)
	}

	binding, err := s.BindOutboundIntegration(ctx, accountID, appID, offer.ID)
	if err != nil || binding.ID != offer.ID || binding.AppID != appID || binding.CredentialConfigured != true {
		t.Fatalf("BindOutboundIntegration = %+v, %v", binding, err)
	}
	// Existing bindings are idempotent and return the effective route ceiling.
	if got, err := s.BindOutboundIntegration(ctx, accountID, appID, offer.ID); err != nil || got.ID != offer.ID {
		t.Fatalf("repeat bind = %+v, %v", got, err)
	}
	bindings, err := s.ListOutboundAppBindings(ctx, accountID, appID)
	if err != nil || len(bindings) != 1 || len(bindings[0].RouteMethods) != 2 || bindings[0].RouteMethods[0] != "GET" {
		t.Fatalf("ListOutboundAppBindings = %+v, %v", bindings, err)
	}
	if err := s.UpdateOutboundBindingPolicy(ctx, accountID, appID, offer.ID, []string{"GET"}, []string{"/v1/items"}); err != nil {
		t.Fatalf("valid binding policy: %v", err)
	}
	if err := s.UpdateOutboundBindingPolicy(ctx, accountID, appID, offer.ID, []string{"PATCH"}, []string{"/v1"}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("policy above integration ceiling = %v, want ErrInvalidArgument", err)
	}
	if err := s.UpdateOutboundBindingPolicy(ctx, accountID, uuid.NewString(), offer.ID, []string{"GET"}, []string{"/v1"}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing binding policy = %v, want ErrNotFound", err)
	}
	bindings, err = s.ListOutboundAppBindings(ctx, accountID, appID)
	if err != nil || len(bindings) != 1 || len(bindings[0].RouteMethods) != 1 || bindings[0].RouteMethods[0] != "GET" || bindings[0].RoutePathPrefixes[0] != "/v1/items" {
		t.Fatalf("updated binding = %+v, %v", bindings, err)
	}
	if _, err := s.ListOutboundAppBindings(ctx, accountID, uuid.NewString()); err != nil {
		t.Fatalf("empty app binding list = %v", err)
	}

	if err := s.DeleteOutboundCredential(ctx, uuid.NewString(), offer.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account credential deletion = %v, want ErrNotFound", err)
	}
	if err := s.DeleteOutboundCredential(ctx, accountID, offer.ID); err != nil {
		t.Fatalf("DeleteOutboundCredential: %v", err)
	}
	// Deleting an already-absent credential for an eligible integration is idempotent.
	if err := s.DeleteOutboundCredential(ctx, accountID, offer.ID); err != nil {
		t.Fatalf("repeat DeleteOutboundCredential: %v", err)
	}
	bindings, err = s.ListOutboundAppBindings(ctx, accountID, appID)
	if err != nil || len(bindings) != 1 || bindings[0].CredentialConfigured {
		t.Fatalf("binding after credential deletion = %+v, %v", bindings, err)
	}

	if _, err := s.BindOutboundIntegration(ctx, uuid.NewString(), appID, offer.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account bind = %v, want ErrNotFound", err)
	}
	if err := s.UnbindOutboundIntegration(ctx, accountID, appID, offer.ID); err != nil {
		t.Fatalf("UnbindOutboundIntegration: %v", err)
	}
	if err := s.UnbindOutboundIntegration(ctx, accountID, appID, offer.ID); err != nil {
		t.Fatalf("repeat unbind: %v", err)
	}
	if err := s.UpdateOutboundBindingPolicy(ctx, accountID, appID, offer.ID, []string{"GET"}, []string{"/v1"}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("policy update after unbind = %v, want ErrNotFound", err)
	}
	if err := s.DeleteOutboundIntegration(ctx, uuid.NewString(), offer.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account integration delete = %v, want ErrNotFound", err)
	}
	if err := s.DeleteOutboundIntegration(ctx, accountID, offer.ID); err != nil {
		t.Fatalf("DeleteOutboundIntegration: %v", err)
	}
	if err := s.DeleteOutboundIntegration(ctx, accountID, offer.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("repeat integration delete = %v, want ErrNotFound", err)
	}
	if _, err := s.GetOutboundIntegrationUsage(ctx, accountID, offer.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("usage after deletion = %v, want ErrNotFound", err)
	}
	if err := s.SetOutboundDailyRequestLimit(ctx, accountID, offer.ID, nil); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("limit after deletion = %v, want ErrNotFound", err)
	}
	if got, err := s.ListOutboundIntegrationOffers(ctx, accountID); err != nil || len(got) != 0 {
		t.Fatalf("offers after deletion = %+v, %v", got, err)
	}
}

func TestPgStore_OutboundIntegrationValidationAndQuota(t *testing.T) {
	s, ctx := pgStore(t)
	accountID, _, _ := seedLiveDeploy(t, s, ctx)
	invalid := pgCustomerOutboundOffer(accountID, "Invalid")
	if _, err := s.CreateOutboundIntegration(ctx, invalid); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("invalid customer offer = %v, want ErrInvalidArgument", err)
	}
	missingAccount := pgCustomerOutboundOffer(uuid.NewString(), "missing")
	if _, err := s.CreateOutboundIntegration(ctx, missingAccount); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing account = %v, want ErrNotFound", err)
	}
	first := pgCustomerOutboundOffer(accountID, "duplicate")
	if _, err := s.CreateOutboundIntegration(ctx, first); err != nil {
		t.Fatalf("create first duplicate fixture: %v", err)
	}
	duplicateName := pgCustomerOutboundOffer(accountID, "duplicate")
	if _, err := s.CreateOutboundIntegration(ctx, duplicateName); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate name = %v, want ErrConflict", err)
	}
	for i := 1; i < state.MaxCustomerOutboundIntegrations; i++ {
		offer := pgCustomerOutboundOffer(accountID, "integration-"+uuid.NewString()[:8])
		if _, err := s.CreateOutboundIntegration(ctx, offer); err != nil {
			t.Fatalf("create integration %d: %v", i, err)
		}
	}
	overLimit := pgCustomerOutboundOffer(accountID, "over-limit")
	if _, err := s.CreateOutboundIntegration(ctx, overLimit); !errors.Is(err, state.ErrOutboundIntegrationLimit) {
		t.Fatalf("integration over quota = %v, want ErrOutboundIntegrationLimit", err)
	}
}
