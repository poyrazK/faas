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

func TestWorkflowIdempotencyIsolatesTenants(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	ctx := context.Background()
	app, err := (*e.Store()).CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: "audit-tenant-idempotency", Type: state.AppTypeFunction, Runtime: "node22", RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	definitions, err := json.Marshal([]api.WorkflowSpec{{Name: "process-order", Steps: []api.WorkflowStepSpec{{Name: "main", Path: "/process-order"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (*e.Store()).CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "registry.example.com/test@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Status: state.DeployLive, Workflows: definitions}); err != nil {
		t.Fatal(err)
	}
	required := true
	if _, err := (*e.Store()).UpdateApp(ctx, app.ID, state.UpdateAppParams{PlatformTenantRequired: &required, SetPlatformTenantRequired: true}); err != nil {
		t.Fatal(err)
	}
	tokens := []string{}
	tenantIDs := []string{}
	for _, name := range []string{"a", "b"} {
		tenant, _, err := (*e.Store()).CreatePlatformTenant(ctx, (*e.Acct()).ID, "audit-"+name, name, 10)
		if err != nil {
			t.Fatal(err)
		}
		consumer, err := (*e.Store()).CreateAPIConsumer(ctx, (*e.Acct()).ID, app.ID, "audit-consumer-"+name, name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := (*e.Store()).LinkPlatformTenantConsumer(ctx, (*e.Acct()).ID, tenant.ID, consumer.ID); err != nil {
			t.Fatal(err)
		}
		issued := e.Do(t, http.MethodPost, "/v1/account/platform-tenants/"+tenant.ID+"/access-tokens", api.CreatePlatformTenantAccessTokenRequest{Name: name, Scopes: []string{api.ScopePlatformTenantInvocationsManage}}, nil)
		if issued.Code != http.StatusCreated {
			t.Fatalf("issue token: %d %s", issued.Code, issued.Body)
		}
		var token api.CreatePlatformTenantAccessTokenResponse
		if err := json.Unmarshal(issued.Body.Bytes(), &token); err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, token.Token)
		tenantIDs = append(tenantIDs, tenant.ID)
	}
	var runs []api.WorkflowRunResponse
	for _, token := range tokens {
		created := e.Do(t, http.MethodPost, "/v1/platform-tenant-self/apps/"+app.Slug+"/workflows/process-order/runs", map[string]string{"order_id": "42"}, map[string]string{"Authorization": "Bearer " + token, "Idempotency-Key": "order-42"})
		if created.Code != http.StatusCreated {
			t.Fatalf("start: %d %s", created.Code, created.Body)
		}
		var run api.WorkflowRunResponse
		if err := json.Unmarshal(created.Body.Bytes(), &run); err != nil {
			t.Fatal(err)
		}
		runs = append(runs, run)
	}
	if runs[1].PlatformTenantID != tenantIDs[1] || runs[1].ID == runs[0].ID {
		t.Fatalf("tenant B received tenant A's run: a_run=%s b_run=%s expected_tenant=%s returned_tenant=%s", runs[0].ID, runs[1].ID, tenantIDs[1], runs[1].PlatformTenantID)
	}
}
