package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOutboundCustomerBindingLifecycle(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := createApp(t, e, "outbound-customer")
	integrationID := uuid.NewString()
	e.store.SeedOutboundIntegrationOffer(state.OutboundIntegrationOffer{
		ID: integrationID, AccountID: e.acct.ID, Name: "stripe-production",
		Origin: "https://api.stripe.com", AllowedMethods: []string{"GET"},
		AllowedPathPrefixes: []string{"/v1/customers"}, Enabled: true,
	})
	other, err := e.store.CreateAccount(context.Background(), "other-outbound@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	otherID := uuid.NewString()
	e.store.SeedOutboundIntegrationOffer(state.OutboundIntegrationOffer{
		ID: otherID, AccountID: other.ID, Name: "other-secret",
		Origin: "https://private.example", AllowedMethods: []string{"POST"},
		AllowedPathPrefixes: []string{"/admin"}, Enabled: true,
	})

	offers := e.do(t, http.MethodGet, "/v1/outbound/integrations", nil, nil)
	if offers.Code != http.StatusOK || strings.Contains(offers.Body.String(), otherID) {
		t.Fatalf("account-scoped offers = %d %s", offers.Code, offers.Body.String())
	}
	var catalog api.OutboundIntegrationOfferList
	if err := json.Unmarshal(offers.Body.Bytes(), &catalog); err != nil || len(catalog.Items) != 1 || catalog.Items[0].ID != integrationID {
		t.Fatalf("catalog = %+v, %v", catalog, err)
	}
	path := "/v1/apps/" + app.Slug + "/outbound-bindings/" + integrationID
	wrong := e.do(t, http.MethodPut, "/v1/apps/"+app.Slug+"/outbound-bindings/"+otherID, nil, nil)
	assertProblem(t, wrong, http.StatusNotFound, api.CodeNotFound)
	created := e.do(t, http.MethodPut, path, nil, nil)
	if created.Code != http.StatusOK {
		t.Fatalf("create binding = %d %s", created.Code, created.Body.String())
	}
	var binding api.OutboundAppBinding
	if err := json.Unmarshal(created.Body.Bytes(), &binding); err != nil || binding.AppID != app.ID || binding.Integration.ID != integrationID || binding.CreatedAt.IsZero() {
		t.Fatalf("binding = %+v, %v", binding, err)
	}
	if strings.Contains(created.Body.String(), "sk_") || strings.Contains(created.Body.String(), "gateway-token") {
		t.Fatal("binding response contains credential material")
	}
	repeated := e.do(t, http.MethodPut, path, nil, nil)
	var repeatedBinding api.OutboundAppBinding
	if repeated.Code != http.StatusOK || json.Unmarshal(repeated.Body.Bytes(), &repeatedBinding) != nil || !repeatedBinding.CreatedAt.Equal(binding.CreatedAt) {
		t.Fatalf("idempotent bind = %d %s", repeated.Code, repeated.Body.String())
	}
	list := e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/outbound-bindings", nil, nil)
	var bindings api.OutboundAppBindingList
	if list.Code != http.StatusOK || json.Unmarshal(list.Body.Bytes(), &bindings) != nil || len(bindings.Items) != 1 {
		t.Fatalf("binding list = %d %s", list.Code, list.Body.String())
	}
	deleted := e.do(t, http.MethodDelete, path, nil, nil)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete binding = %d %s", deleted.Code, deleted.Body.String())
	}
	list = e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/outbound-bindings", nil, nil)
	if list.Code != http.StatusOK || json.Unmarshal(list.Body.Bytes(), &bindings) != nil || len(bindings.Items) != 0 {
		t.Fatalf("binding list after delete = %d %s", list.Code, list.Body.String())
	}
}

func TestOutboundCustomerCredentialLifecycle(t *testing.T) {
	e := setup(t, api.PlanPro)
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	previous := outboundCredentialRecipient
	outboundCredentialRecipient = func() *age.X25519Recipient { return identity.Recipient() }
	t.Cleanup(func() { outboundCredentialRecipient = previous })
	id := uuid.NewString()
	e.store.SeedOutboundIntegrationOffer(state.OutboundIntegrationOffer{
		ID: id, AccountID: e.acct.ID, Name: "customer-key", Origin: "https://api.example.com",
		AllowedMethods: []string{"GET"}, AllowedPathPrefixes: []string{"/v1"},
		Enabled: true, CredentialSource: "customer_sealed",
	})
	path := "/v1/outbound/integrations/" + id + "/credential"
	bad := e.do(t, http.MethodPut, path, api.PutOutboundCredentialRequest{Authorization: "Bearer bad\nvalue"}, nil)
	assertProblem(t, bad, http.StatusBadRequest, api.CodeValidation)
	other, err := e.store.CreateAccount(context.Background(), "outbound-credential-other@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	otherID := uuid.NewString()
	e.store.SeedOutboundIntegrationOffer(state.OutboundIntegrationOffer{
		ID: otherID, AccountID: other.ID, Name: "other-key", Origin: "https://private.example",
		AllowedMethods: []string{"GET"}, AllowedPathPrefixes: []string{"/v1"},
		Enabled: true, CredentialSource: "customer_sealed",
	})
	wrong := e.do(t, http.MethodPut, "/v1/outbound/integrations/"+otherID+"/credential",
		api.PutOutboundCredentialRequest{Authorization: "Bearer sk_foreign"}, nil)
	assertProblem(t, wrong, http.StatusNotFound, api.CodeNotFound)
	operatorID := uuid.NewString()
	e.store.SeedOutboundIntegrationOffer(state.OutboundIntegrationOffer{
		ID: operatorID, AccountID: e.acct.ID, Name: "operator-key", Origin: "https://api.operator.example",
		AllowedMethods: []string{"GET"}, AllowedPathPrefixes: []string{"/v1"}, Enabled: true,
	})
	operatorWrite := e.do(t, http.MethodPut, "/v1/outbound/integrations/"+operatorID+"/credential",
		api.PutOutboundCredentialRequest{Authorization: "Bearer sk_override"}, nil)
	assertProblem(t, operatorWrite, http.StatusNotFound, api.CodeNotFound)
	for _, value := range []string{"Bearer sk_first", "Bearer sk_rotated"} {
		response := e.do(t, http.MethodPut, path, api.PutOutboundCredentialRequest{Authorization: value}, nil)
		if response.Code != http.StatusNoContent || strings.Contains(response.Body.String(), value) {
			t.Fatalf("set credential = %d %s", response.Code, response.Body.String())
		}
	}
	list := e.do(t, http.MethodGet, "/v1/outbound/integrations", nil, nil)
	var offers api.OutboundIntegrationOfferList
	if list.Code != http.StatusOK || json.Unmarshal(list.Body.Bytes(), &offers) != nil || len(offers.Items) != 2 || !offers.Items[0].CredentialConfigured {
		t.Fatalf("credential status = %d %s", list.Code, list.Body.String())
	}
	if strings.Contains(list.Body.String(), "sk_first") || strings.Contains(list.Body.String(), "sk_rotated") {
		t.Fatal("credential escaped through offer metadata")
	}
	deleted := e.do(t, http.MethodDelete, path, nil, nil)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete credential = %d %s", deleted.Code, deleted.Body.String())
	}
	list = e.do(t, http.MethodGet, "/v1/outbound/integrations", nil, nil)
	if list.Code != http.StatusOK || json.Unmarshal(list.Body.Bytes(), &offers) != nil || len(offers.Items) != 2 || offers.Items[0].CredentialConfigured {
		t.Fatalf("revoked credential status = %d %s", list.Code, list.Body.String())
	}
}
