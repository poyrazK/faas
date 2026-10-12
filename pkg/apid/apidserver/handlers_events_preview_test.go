package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPreviewEventUsesRouterMatcherWithoutPublishing(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	matchedAppID := apidserver.MustSeedApp(t, e, "preview-matched")
	filteredAppID := apidserver.MustSeedApp(t, e, "preview-filtered")
	unrelatedAppID := apidserver.MustSeedApp(t, e, "preview-unrelated")
	for _, subscription := range []struct {
		appID  string
		source string
		typ    string
		filter string
	}{
		{matchedAppID, "billing.*", "invoice.paid", `{"data":{"amount":{"$gt":100}}}`},
		{filteredAppID, "billing.*", "invoice.paid", `{"data":{"amount":{"$gt":200}}}`},
		{unrelatedAppID, "shipping.*", "invoice.paid", `{}`},
	} {
		if _, _, err := (*e.Store()).UpsertEventSubscription(context.Background(), (*e.Acct()).ID, subscription.appID, subscription.source, subscription.typ, json.RawMessage(subscription.filter)); err != nil {
			t.Fatalf("seed subscription for %s: %v", subscription.appID, err)
		}
	}

	rec := e.Do(t, http.MethodPost, "/v1/events:preview", api.PreviewEventRequest{
		ID:     "preview-invoice-1",
		Source: "billing.stripe",
		Type:   "invoice.paid",
		Data:   json.RawMessage(`{"amount":150}`),
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var got api.PreviewEventResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if got.EventID != "preview-invoice-1" || got.CandidateCount != 2 || got.MatchedCount != 1 || got.FilterMismatchCount != 1 || got.OtherMismatchCount != 0 {
		t.Fatalf("preview summary = %+v", got)
	}
	if len(got.Matches) != 1 || got.Matches[0].AppSlug != "preview-matched" || got.Matches[0].Reason != "would_deliver" {
		t.Fatalf("matches = %+v", got.Matches)
	}
	if len(got.NonMatches) != 1 || got.NonMatches[0].AppSlug != "preview-filtered" || got.NonMatches[0].Reason != "content_filter_mismatch" {
		t.Fatalf("non-matches = %+v", got.NonMatches)
	}

	rows, err := (*e.Store()).ListEvents(context.Background(), (*e.Acct()).ID, 100)
	if err != nil {
		t.Fatalf("list account events: %v", err)
	}
	for _, row := range rows {
		if row.Kind == "event.published" {
			t.Fatalf("preview persisted a published event: %+v", row)
		}
	}
}

func TestPreviewEventRejectsInvalidEnvelope(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	rec := e.Do(t, http.MethodPost, "/v1/events:preview", api.PreviewEventRequest{
		Source: "",
		Type:   "invoice.paid",
		Data:   json.RawMessage(`{}`),
	}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestPreviewEventDoesNotRevealOtherAccountsSubscriptions(t *testing.T) {
	owner := apidserver.Setup(t, api.PlanPro)
	appID := apidserver.MustSeedApp(t, owner, "private-preview-worker")
	if _, _, err := (*owner.Store()).UpsertEventSubscription(context.Background(), (*owner.Acct()).ID, appID,
		"billing.*", "invoice.paid", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("seed private subscription: %v", err)
	}
	foreignAcct, err := (*owner.Store()).CreateAccount(context.Background(), "preview-foreign@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("create foreign account: %v", err)
	}
	foreignToken, foreignHash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatalf("generate foreign key: %v", err)
	}
	if _, err := (*owner.Store()).CreateAPIKey(context.Background(), foreignAcct.ID, foreignHash, "foreign", api.ScopesAdminOnly); err != nil {
		t.Fatalf("create foreign key: %v", err)
	}
	foreign := owner
	(*foreign.Acct()) = foreignAcct
	(*foreign.Key()) = foreignToken

	rec := foreign.Do(t, http.MethodPost, "/v1/events:preview", api.PreviewEventRequest{
		Source: "billing.stripe",
		Type:   "invoice.paid",
		Data:   json.RawMessage(`{}`),
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got api.PreviewEventResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if got.CandidateCount != 0 || len(got.Matches) != 0 || len(got.NonMatches) != 0 {
		t.Fatalf("preview revealed foreign subscriptions: %+v", got)
	}
}

var _ state.EventSubscriptionMatcherStore = (*state.MemStore)(nil)
