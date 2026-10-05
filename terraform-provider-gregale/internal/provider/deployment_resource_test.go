package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestDeploymentResourceSchemaIsValid(t *testing.T) {
	var resp resource.SchemaResponse
	newDeploymentResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
		t.Fatalf("deployment resource schema diagnostics: %v", diags)
	}
}

// A create-before-destroy replacement shares the scoped source reservation.
// Forgetting the old deployment must not release the replacement's ownership.
func TestDeploymentRemovalRetainsSourceOwnership(t *testing.T) {
	for _, operation := range []string{"live", "superseded", "pending", "missing"} {
		t.Run(operation, func(t *testing.T) {
			ctx := context.Background()
			var requests []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				requests = append(requests, req.Method+" "+req.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				if operation == "missing" && req.Method == http.MethodGet && req.URL.Path == "/v1/deployments/old" {
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(`{"code":"not_found"}`))
					return
				}
				if operation == "pending" && req.Method == http.MethodPost && req.URL.Path == "/v1/apps/shop/deployments/old/cancel" {
					_, _ = w.Write([]byte(`{"id":"old","status":"cancelled"}`))
					return
				}
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer server.Close()
			r := &deploymentResource{client: &client{baseURL: server.URL, token: "test", http: server.Client()}}
			var schemaResp resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
			state := tfsdk.State{Schema: schemaResp.Schema, Raw: tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil)}
			out := deploymentResponse{ID: "old", AppID: "app-1", Status: operation, Scope: "production"}
			fallback := deploymentModel{AppSlug: types.StringValue("shop")}
			if diags := setDeploymentModel(ctx, &state, out, deploymentURLResponse{}, fallback); diags.HasError() {
				t.Fatalf("setDeploymentModel: %v", diags)
			}
			if operation == "missing" {
				resp := resource.ReadResponse{State: state}
				r.Read(ctx, resource.ReadRequest{State: state}, &resp)
				if resp.Diagnostics.HasError() || !resp.State.Raw.IsNull() {
					t.Fatalf("read missing deployment: %v, state=%v", resp.Diagnostics, resp.State.Raw)
				}
			} else {
				var resp resource.DeleteResponse
				r.Delete(ctx, resource.DeleteRequest{State: state}, &resp)
				if resp.Diagnostics.HasError() {
					t.Fatalf("delete deployment: %v", resp.Diagnostics)
				}
			}
			want := ""
			if operation == "pending" {
				want = "POST /v1/apps/shop/deployments/old/cancel"
			} else if operation == "missing" {
				want = "GET /v1/deployments/old"
			}
			if (want == "" && len(requests) != 0) || (want != "" && (len(requests) != 1 || requests[0] != want)) {
				t.Fatalf("requests = %v; want only %q, with no ownership release", requests, want)
			}
		})
	}
}
