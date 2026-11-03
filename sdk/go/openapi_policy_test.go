package faas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/poyrazK/faas/sdk/go/preview"

	faas "github.com/poyrazK/faas/sdk/go"
)

// adr: 20261028110044 — the Go SDK previews and applies OpenAPI route policy
// with the plan token the server issued.
func TestOpenAPIPolicyClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/apps/demo/openapi/preview":
			_, _ = w.Write([]byte(`{"app_id":"11111111-1111-4111-8111-111111111111","source":"upload","observed_available":true,"observed_source":"fleet","routes":[{"path":"/orders","method":"POST","status":"declared_only","declared":true,"observed":false,"covered":false}]}`))
		case r.Method == "POST" && r.URL.Path == "/v1/apps/demo/openapi/apply":
			var request preview.ApplyAppOpenAPIPolicyRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			if !request.Confirm {
				_, _ = w.Write([]byte(`{"app_id":"11111111-1111-4111-8111-111111111111","match_host":"demo.gregale.app","preview_sha256":"abc","planned":true,"suggestions":[{"path":"/orders","methods":["POST"],"kind":"validate","action":{"schema":{}}}],"applied":[],"applied_count":0}`))
				return
			}
			if request.PreviewSHA256 != "abc" {
				t.Errorf("apply token = %q", request.PreviewSHA256)
			}
			_, _ = w.Write([]byte(`{"app_id":"11111111-1111-4111-8111-111111111111","match_host":"demo.gregale.app","preview_sha256":"abc","planned":false,"suggestions":[],"applied":[{"id":"22222222-2222-4222-8222-222222222222","match_host":"demo.gregale.app","match_path":"/orders","match_methods":["POST"],"priority":100,"enabled":true,"kind":"validate","action":{"schema":{}},"mode":"enforce"}],"applied_count":1}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	c := preview.Wrap(client)
	ctx := context.Background()
	routes, err := c.PreviewAppOpenAPIPolicy(ctx, "demo")
	if err != nil || len(routes.Routes) != 1 || routes.Routes[0].Status != "declared_only" || routes.Routes[0].Covered {
		t.Fatalf("preview = %+v, %v", routes, err)
	}
	plan, err := c.ApplyAppOpenAPIPolicy(ctx, "demo", preview.ApplyAppOpenAPIPolicyRequest{})
	if err != nil || !plan.Planned || plan.PreviewSHA256 != "abc" || len(plan.Suggestions) != 1 {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	applied, err := c.ApplyAppOpenAPIPolicy(ctx, "demo", preview.ApplyAppOpenAPIPolicyRequest{Confirm: true, PreviewSHA256: plan.PreviewSHA256})
	if err != nil || applied.Planned || applied.AppliedCount != 1 || len(applied.Applied) != 1 || applied.Applied[0].MatchPath != "/orders" {
		t.Fatalf("apply = %+v, %v", applied, err)
	}
}
