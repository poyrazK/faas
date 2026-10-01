package faas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestRegionalProjectScanApply(t *testing.T) {
	for _, apply := range []bool{false, true} {
		t.Run(map[bool]string{false: "scan", true: "apply"}[apply], func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseMultipartForm(1024); err != nil {
					t.Error(err)
					return
				}
				if r.FormValue("compute_region") != "eu-fsn" || r.FormValue("platform_tenant_required") != "true" {
					t.Errorf("multipart fields=%+v", r.MultipartForm.Value)
				}
				if r.Header.Get("Authorization") != "Bearer token" {
					t.Error("missing authentication")
				}
				if apply && (r.Header.Get("Idempotency-Key") != "project-attempt" || r.URL.Query().Get("plan_token") != "plan-token") {
					t.Error("lost apply identity")
				}
				_ = json.NewEncoder(w).Encode(faas.ApplyResponse{PlanResponse: faas.PlanResponse{CanApply: true, ComputeRegion: "eu-fsn", Workloads: []faas.PlanWorkload{{Name: "customer-api", ComputeRegion: "eu-fsn"}}}})
			}))
			defer srv.Close()
			client, err := faas.NewClient(srv.URL, "token")
			if err != nil {
				t.Fatal(err)
			}
			required := true
			opts := faas.ProjectDeploymentOptions{ComputeRegion: "eu-fsn", PlatformTenantRequired: &required}
			ctx := faas.WithIdempotencyKey(context.Background(), "project-attempt")
			var plan faas.PlanResponse
			if apply {
				var out faas.ApplyResponse
				out, err = client.ApplyProjectPlanWithBindingOptions(ctx, "plan-token", strings.NewReader("source"), "project.tar.gz", "project", "", "main", 0, nil, nil, false, false, "", "", opts)
				plan = out.PlanResponse
			} else {
				plan, err = client.ScanProjectWithBindingOptions(ctx, strings.NewReader("source"), "project.tar.gz", "project", "", "main", 0, nil, nil, false, false, "", opts)
			}
			if err != nil || plan.ComputeRegion != "eu-fsn" || len(plan.Workloads) != 1 || plan.Workloads[0].ComputeRegion != "eu-fsn" {
				t.Fatalf("plan=%+v err=%v", plan, err)
			}
		})
	}
}
