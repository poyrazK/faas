package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPlatformTenantEventStartsScopedWorkflowAndExposesReceipt(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := context.Background()
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, Slug: "tenant-event-start", Type: state.AppTypeFunction,
		Runtime: "node22", RAMMB: 256, PlatformTenantRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	definitions, err := json.Marshal([]api.WorkflowSpec{{Name: "on-payment", Trigger: &api.WorkflowTriggerSpec{
		Type: "event", Source: "billing.*", EventType: "invoice.paid",
	}, Steps: []api.WorkflowStepSpec{{Name: "process", Path: "/process-payment"}}}})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
		ImageDigest: "registry.example.com/tenant-event@sha256:" + strings.Repeat("b", 64), Status: state.DeployPending, Workflows: definitions})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}
	tenant, _, err := e.store.CreatePlatformTenant(ctx, e.acct.ID, "tenant-event-customer", "Tenant event customer", 20)
	if err != nil {
		t.Fatal(err)
	}
	seedPlatformTenantConsumer(t, e, app.Slug, tenant.ID)
	otherTenant, _, err := e.store.CreatePlatformTenant(ctx, e.acct.ID, "tenant-event-other", "Other tenant", 20)
	if err != nil {
		t.Fatal(err)
	}
	issueToken := func(tenantID, name string, scopes ...string) string {
		t.Helper()
		issued := e.do(t, http.MethodPost, "/v1/account/platform-tenants/"+tenantID+"/access-tokens",
			api.CreatePlatformTenantAccessTokenRequest{Name: name, Scopes: scopes}, nil)
		if issued.Code != http.StatusCreated {
			t.Fatalf("issue tenant token: %d %s", issued.Code, issued.Body)
		}
		var token api.CreatePlatformTenantAccessTokenResponse
		if err := json.Unmarshal(issued.Body.Bytes(), &token); err != nil {
			t.Fatal(err)
		}
		return token.Token
	}
	tenantToken := issueToken(tenant.ID, "event publisher", api.ScopePlatformTenantEventsManage, api.ScopePlatformTenantEventsRead)
	readOnlyToken := issueToken(tenant.ID, "event reader", api.ScopePlatformTenantEventsRead)
	foreignToken := issueToken(otherTenant.ID, "foreign event publisher", api.ScopePlatformTenantEventsManage)
	path := "/v1/platform-tenant-self/apps/" + app.Slug + "/events:publish"
	headers := map[string]string{"Authorization": "Bearer " + tenantToken}
	request := map[string]any{"id": "invoice-42", "source": "billing.stripe", "type": "invoice.paid", "data": map[string]any{"amount": 125},
		"appid": "00000000-0000-0000-0000-000000000001", "platformtenantid": otherTenant.ID, "tenanteventid": "forged"}
	first := e.do(t, http.MethodPost, path, request, headers)
	var accepted api.PublishEventResponse
	if first.Code != http.StatusAccepted || json.Unmarshal(first.Body.Bytes(), &accepted) != nil || accepted.ID == "" || accepted.ID == "invoice-42" || accepted.ClientEventID != "invoice-42" {
		t.Fatalf("publish tenant event: %d %s", first.Code, first.Body)
	}
	if first.Header().Get("Location") != accepted.ReceiptURL || accepted.AccountID != "" {
		t.Fatalf("tenant response leaked account identity or mismatched receipt URL: %d %s", first.Code, first.Body)
	}
	duplicate := e.do(t, http.MethodPost, path, request, headers)
	var repeated api.PublishEventResponse
	if duplicate.Code != http.StatusAccepted || json.Unmarshal(duplicate.Body.Bytes(), &repeated) != nil || repeated.ID != accepted.ID {
		t.Fatalf("same event retry: %d %s", duplicate.Code, duplicate.Body)
	}
	changed := map[string]any{"id": "invoice-42", "source": "billing.stripe", "type": "invoice.paid", "data": map[string]any{"amount": 126}}
	conflict := e.do(t, http.MethodPost, path, changed, headers)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("changed-content retry = %d %s", conflict.Code, conflict.Body)
	}
	if denied := e.do(t, http.MethodPost, path, request, map[string]string{"Authorization": "Bearer " + readOnlyToken}); denied.Code != http.StatusForbidden {
		t.Fatalf("read-only token published event: %d %s", denied.Code, denied.Body)
	}
	if denied := e.do(t, http.MethodPost, path, request, map[string]string{"Authorization": "Bearer " + foreignToken}); denied.Code != http.StatusNotFound {
		t.Fatalf("unlinked tenant published to app: %d %s", denied.Code, denied.Body)
	}

	work, err := e.store.ClaimDuePublishedEvent(ctx, time.Now().UTC())
	if err != nil || work == nil || len(work.RecipientSnapshot) != 1 {
		t.Fatalf("tenant event fanout=%+v err=%v", work, err)
	}
	recipient := work.RecipientSnapshot[0]
	if recipient.AppID != app.ID || recipient.AccountID != e.acct.ID || recipient.PlatformTenantID != tenant.ID || recipient.Workflow == nil {
		t.Fatalf("captured recipient trusted caller identity or widened app scope: %+v", recipient)
	}
	runID, err := e.store.AdmitEventWorkflow(ctx, work.ID, work.ClaimToken, recipient.ID)
	if err != nil || runID == "" {
		t.Fatalf("admit tenant workflow: run=%q err=%v", runID, err)
	}
	run, err := e.store.GetWorkflowRun(ctx, runID)
	if err != nil || run.PlatformTenantID != tenant.ID || run.AppID != app.ID || run.WorkflowName != "on-payment" {
		t.Fatalf("tenant workflow run=%+v err=%v", run, err)
	}
	receiptPath := "/v1/platform-tenant-self/apps/" + app.Slug + "/events/receipts/" + accepted.ID + "?source=billing.stripe"
	receiptRec := e.do(t, http.MethodGet, receiptPath, nil, headers)
	var receipt api.EventReceiptResponse
	if receiptRec.Code != http.StatusOK || json.Unmarshal(receiptRec.Body.Bytes(), &receipt) != nil || receipt.ClientEventID != "invoice-42" || len(receipt.Recipients) != 1 ||
		receipt.Recipients[0].WorkflowRunID != runID || receipt.Recipients[0].WorkflowRunStatus != string(run.Status) || len(receipt.Recipients[0].RecoveryActions) != 0 {
		t.Fatalf("tenant event receipt: %d %s", receiptRec.Code, receiptRec.Body)
	}
	if denied := e.do(t, http.MethodGet, receiptPath, nil, map[string]string{"Authorization": "Bearer " + issueToken(tenant.ID, "manage only", api.ScopePlatformTenantEventsManage)}); denied.Code != http.StatusForbidden {
		t.Fatalf("manage-only token read receipt: %d %s", denied.Code, denied.Body)
	}
}
