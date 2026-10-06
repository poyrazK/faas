package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestQueueWorkPreservesBoundedFlagContext(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "flag-queue")
	tenant, _, err := e.store.CreatePlatformTenant(context.Background(), e.acct.ID, "flag-customer", "Flag customer", 100)
	if err != nil {
		t.Fatal(err)
	}
	contextFor := func(customerID string) string {
		t.Helper()
		encoded, err := flags.EncodePropagationHeader(flags.PropagationContext{
			Version: flags.PropagationContextVersion, CustomerID: customerID,
			Decisions: []flags.PropagationDecision{{
				Decision: flags.Decision{Flag: "new-export", Value: true, ConfigVersion: 7, Reason: "default", Source: "configuration"},
				Origin:   flags.EvidenceOrigin{AppID: uuid.NewString(), EnvironmentID: uuid.NewString()},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	flagContext := contextFor(tenant.ID)

	queueResponse := e.do(t, http.MethodPost, "/v1/apps/flag-queue/queues/send", api.QueueSendRequest{
		Payload: json.RawMessage(`{"kind":"export"}`), FlagContext: flagContext,
	}, nil)
	if queueResponse.Code != http.StatusCreated {
		t.Fatalf("queue send: %d %s", queueResponse.Code, queueResponse.Body)
	}
	var receipt api.QueueSendResponse
	if err := json.Unmarshal(queueResponse.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	queued, err := e.store.InvocationByID(context.Background(), receipt.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertInvocationFlagContext(t, queued, tenant.ID, flagContext)

	inboxResponse := e.do(t, http.MethodPost, "/v1/apps/flag-queue/inbox", api.SendAppMessageRequest{
		Type: "export.requested", Data: json.RawMessage(`{"customer":"flag-customer"}`), FlagContext: flagContext,
	}, nil)
	if inboxResponse.Code != http.StatusAccepted {
		t.Fatalf("app inbox: %d %s", inboxResponse.Code, inboxResponse.Body)
	}
	var inbox api.SendAppMessageResponse
	if err := json.Unmarshal(inboxResponse.Body.Bytes(), &inbox); err != nil {
		t.Fatal(err)
	}
	inboxInvocation, err := e.store.InvocationByID(context.Background(), inbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertInvocationFlagContext(t, inboxInvocation, tenant.ID, flagContext)

	invalid := e.do(t, http.MethodPost, "/v1/apps/flag-queue/queues/send", api.QueueSendRequest{
		Payload: json.RawMessage(`{"kind":"export"}`), FlagContext: "not-base64",
	}, nil)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid flag context status=%d body=%s", invalid.Code, invalid.Body)
	}

	otherAccount, err := e.store.CreateAccount(context.Background(), "foreign-flag-context@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	foreignTenant, _, err := e.store.CreatePlatformTenant(context.Background(), otherAccount.ID, "foreign", "Foreign", 100)
	if err != nil {
		t.Fatal(err)
	}
	foreign := e.do(t, http.MethodPost, "/v1/apps/flag-queue/queues/send", api.QueueSendRequest{
		Payload: json.RawMessage(`{"kind":"export"}`), FlagContext: contextFor(foreignTenant.ID),
	}, nil)
	if foreign.Code != http.StatusBadRequest {
		t.Fatalf("cross-account flag context status=%d body=%s", foreign.Code, foreign.Body)
	}

	if _, err := e.store.SetPlatformTenantStatus(context.Background(), e.acct.ID, tenant.ID, state.PlatformTenantSuspended); err != nil {
		t.Fatal(err)
	}
	suspended := e.do(t, http.MethodPost, "/v1/apps/flag-queue/queues/send", api.QueueSendRequest{
		Payload: json.RawMessage(`{"kind":"export"}`), FlagContext: flagContext,
	}, nil)
	if suspended.Code != http.StatusForbidden {
		t.Fatalf("suspended flag customer status=%d body=%s", suspended.Code, suspended.Body)
	}
}

func assertInvocationFlagContext(t *testing.T, inv state.Invocation, tenantID, want string) {
	t.Helper()
	if inv.PlatformTenantID != tenantID {
		t.Fatalf("invocation tenant=%q, want %q", inv.PlatformTenantID, tenantID)
	}
	var headers map[string]string
	if err := json.Unmarshal(inv.Headers, &headers); err != nil {
		t.Fatal(err)
	}
	if headers[api.FlagContextHeader] != want {
		t.Fatalf("invocation flag context=%q, want canonical context", headers[api.FlagContextHeader])
	}
}
