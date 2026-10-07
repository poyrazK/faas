// adr: 521
package faas_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestOperationAccountOperatorRoutes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/apps/exports/operations":
			q := r.URL.Query()
			if q.Get("scope") != "production" || q.Get("tenant_id") != "customer" || q.Get("limit") != "2" || q.Get("cursor") != "opaque" || q.Get("state") != "succeeded" || q.Get("name") != "export" || q.Has("app_id") {
				t.Errorf("account selectors %v", q)
			}
			writeOperationJSON(w, 200, `{"operations":[{"id":"operation","platform_tenant_id":"customer","completion_delivery":{"state":"dead","attempts":7}}],"next_cursor":"next"}`)
		case "GET /v1/apps/exports/operations/operation/events":
			if r.URL.Query().Get("after") != "7" {
				t.Error("event watermark lost")
			}
			writeOperationJSON(w, 200, `{"events":[],"latest_sequence":9,"resync_required":true}`)
		case "GET /v1/apps/exports/operations/operation/executions":
			if r.URL.Query().Get("after") != "1" || r.URL.Query().Get("limit") != "2" {
				t.Error("generation pagination lost")
			}
			writeOperationJSON(w, 200, `{"executions":[{"generation":2,"invocation_id":"execution","state":"completed","attempts":1}],"next_generation":2}`)
		case "POST /v1/apps/exports/operations/operation/retry-delivery":
			writeOperationJSON(w, 200, `{"id":"operation","state":"succeeded","generation":2,"completion_delivery":{"state":"pending","attempts":0}}`)
		default:
			t.Errorf("unexpected account route %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := operationClient(t, server)
	page, err := client.ListAccountOperations(context.Background(), "exports", faas.OperationListOptions{Scope: "production", TenantID: "customer", Name: "export", State: faas.OperationSucceeded, Limit: 2, Cursor: "opaque"})
	if err != nil || len(page.Operations) != 1 || page.Operations[0].PlatformTenantID != "customer" || page.NextCursor != "next" {
		t.Fatalf("list %+v %v", page, err)
	}
	events, err := client.GetAccountOperationEvents(context.Background(), "exports", "operation", 7)
	if err != nil || !events.ResyncRequired {
		t.Fatalf("events %+v %v", events, err)
	}
	executions, err := client.GetOperationExecutions(context.Background(), "exports", "operation", 1, 2)
	if err != nil || executions.NextGeneration != 2 || executions.Executions[0].Attempts != 1 {
		t.Fatalf("executions %+v %v", executions, err)
	}
	op, err := client.RetryOperationDelivery(context.Background(), "exports", "operation")
	if err != nil || op.State != faas.OperationSucceeded || op.Generation != 2 || op.CompletionDelivery.State != "pending" {
		t.Fatalf("notification retry %+v %v", op, err)
	}
}

// adr: 521
func TestOperationDefinitionDiscoveryAndTenantIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/apps/exports/deployments/deployment/operation-definitions":
			writeOperationJSON(w, 200, `{"definitions":[{"id":"definition","name":"export","deployment_id":"deployment","revision":"immutable","scope":"production"}]}`)
		case "/v1/apps/exports/deployments/deployment/operation-definitions/export":
			writeOperationJSON(w, 200, `{"id":"definition","deployment_id":"deployment","revision":"immutable","spec":{"input_schema":{"type":"object"}}}`)
		case "/v1/platform-tenant-self/customer-operations/identity":
			writeOperationJSON(w, 200, `{"account_id":"account","platform_tenant_id":"tenant"}`)
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := operationClient(t, server)
	page, err := client.ListOperationDefinitions(context.Background(), "exports", "deployment")
	if err != nil || len(page.Definitions) != 1 || page.Definitions[0].Revision != "immutable" {
		t.Fatalf("definitions %+v %v", page, err)
	}
	definition, err := client.GetOperationDefinition(context.Background(), "exports", "deployment", "export")
	if err != nil || definition.ID != "definition" || string(definition.Spec.InputSchema) != `{"type":"object"}` {
		t.Fatalf("definition %+v %v", definition, err)
	}
	identity, err := client.GetPlatformTenantSelfOperationIdentity(context.Background())
	if err != nil || identity.PlatformTenantID != "tenant" {
		t.Fatalf("identity %+v %v", identity, err)
	}
}

func TestOperationDoctorScopedReadOnlyContract(t *testing.T) {
	const definitionID = "11111111-1111-4111-8111-111111111111"
	// adr: 521 — delivery warnings never determine diagnostic submission state.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/apps/exports/deployments/"+definitionID+"/operation-doctor" || r.URL.Query().Get("tenant_id") != "tenant+selector" || r.URL.Query().Get("name") != "export name" || r.Header.Get("Authorization") != "Bearer operator-token" {
			t.Errorf("doctor wire %s %s", r.Method, r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"app_id":"app","scope":"production","deployment_id":"dep","platform_tenant_id":"tenant","plan":"pro","observed_at":"2026-10-05T13:00:00Z","observation_scope":"responding_api_node","submission_state":"eligible","checks":[{"check":"preview","status":"observed","impact":"submission","code":"preview_cohort_observed","message":"Observed."},{"check":"completion_destination","status":"warning","impact":"delivery","code":"completion_destination_disabled","message":"Disabled."},{"check":"native_lifecycle","status":"unknown","impact":"qualification","code":"native_lifecycle_unverified","message":"Unverified."},{"check":"execution_preview","status":"observed","impact":"submission","code":"preview_cohort_observed","message":"Job allowed.","name":"export name","execution_kind":"job"}]}`)
	}))
	defer srv.Close()
	client, err := faas.NewClient(srv.URL, "operator-token")
	if err != nil {
		t.Fatal(err)
	}
	r, err := client.GetOperationDoctor(context.Background(), "exports", definitionID, "tenant+selector", "export name")
	if err != nil || r.SubmissionState != "eligible" || r.ObservedSubmissionState() != "eligible" || len(r.Checks) != 4 || r.Checks[1].Status != "warning" || r.Checks[2].Status != "unknown" || r.Checks[3].ExecutionKind != "job" {
		t.Fatal("doctor contract", r, err)
	}
}
