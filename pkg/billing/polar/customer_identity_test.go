package polar

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

const emailTakenBody = `{"error":"PolarRequestValidationError","detail":[{"type":"value_error","loc":["body","email"],"msg":"A customer with this email address already exists.","input":"team@example.com"}]}`

// Polar keeps one customer per email, so a second subject with the same
// billing email reports the customer that holds it and that customer's
// subject.
// adr: 20261103131742
func TestCreateCustomerReportsTheCustomerHoldingTheEmail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/customers/external/org-2":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/customers":
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = io.WriteString(w, emailTakenBody)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/customers/" && r.URL.Query().Get("email") == "team@example.com":
			_, _ = io.WriteString(w, `{"items":[{"id":"cus-1","external_id":"acct-1"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	p, err := NewProvider(testConfig(server.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.CreateCustomer(context.Background(), state.Account{ID: "org-2", Email: "team@example.com"})
	var inUse *billing.CustomerEmailInUseError
	if !errors.As(err, &inUse) || !errors.Is(err, billing.ErrCustomerEmailInUse) {
		t.Fatalf("CreateCustomer error = %v, want CustomerEmailInUseError", err)
	}
	if inUse.CustomerID != "cus-1" || inUse.ExternalID != "acct-1" || inUse.Email != "team@example.com" {
		t.Fatalf("in-use error = %+v", inUse)
	}
}

// An organization's billing email follows the organization: the Polar
// customer moves to it, and an email another customer holds is reported.
// adr: 20261103131742
func TestUpdateCustomerEmailMovesTheCustomer(t *testing.T) {
	var patched map[string]any
	taken := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/v1/customers/cus-9":
			if taken {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = io.WriteString(w, emailTakenBody)
				return
			}
			if err := json.NewDecoder(r.Body).Decode(&patched); err != nil {
				t.Fatal(err)
			}
			_, _ = io.WriteString(w, `{"id":"cus-9"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/customers/":
			_, _ = io.WriteString(w, `{"items":[{"id":"cus-1","external_id":"acct-1"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	p, err := NewProvider(testConfig(server.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	org := state.Account{ID: "org-9", Email: "billing@example.com", ProviderCustomerID: "cus-9"}
	if err := p.UpdateCustomerEmail(context.Background(), org); err != nil {
		t.Fatalf("UpdateCustomerEmail: %v", err)
	}
	if patched["email"] != "billing@example.com" {
		t.Fatalf("patched body = %v", patched)
	}
	taken = true
	if err := p.UpdateCustomerEmail(context.Background(), org); !errors.Is(err, billing.ErrCustomerEmailInUse) {
		t.Fatalf("UpdateCustomerEmail on a taken email = %v, want ErrCustomerEmailInUse", err)
	}
}

// Deleting a deleted subject's leftover customer frees its email; a customer
// already gone counts as deleted.
// adr: 20261103131742
func TestDeleteCustomerToleratesAGoneCustomer(t *testing.T) {
	var deleted []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.NotFound(w, r)
			return
		}
		deleted = append(deleted, r.URL.Path)
		if r.URL.Path == "/v1/customers/cus-gone" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	p, err := NewProvider(testConfig(server.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"cus-1", "cus-gone"} {
		if err := p.DeleteCustomer(context.Background(), id); err != nil {
			t.Fatalf("DeleteCustomer(%s): %v", id, err)
		}
	}
	if len(deleted) != 2 {
		t.Fatalf("deletes = %v", deleted)
	}
}

// An upgrade of an existing subscription is charged and applied at once,
// because the customer portal no longer switches plans; a downgrade waits
// for the period end.
// adr: 20261103131742
func TestChangeSubscriptionPlanChargesAnUpgradeNow(t *testing.T) {
	var proration any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		proration = body["proration_behavior"]
		_, _ = io.WriteString(w, `{"id":"sub-1","current_period_end":"2026-09-30T00:00:00Z"}`)
	}))
	defer server.Close()
	p, err := NewProvider(testConfig(server.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.ChangeSubscriptionPlan(context.Background(), state.Account{ID: "acct-1", Plan: api.PlanHobby, StripeSubscriptionItem: "sub-1"}, api.PlanScale); err != nil {
		t.Fatal(err)
	}
	if proration != "invoice" {
		t.Fatalf("upgrade proration = %v, want invoice", proration)
	}
}
