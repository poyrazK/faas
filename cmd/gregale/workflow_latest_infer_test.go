package main_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// `workflows steps latest` needed --workflow-name even for an app with one
// workflow (Hunt 11 H11-29). The name is inferred from recent runs when they
// all belong to one workflow; several workflows still need it.
func TestInferLatestWorkflowName(t *testing.T) {
	tests := []struct {
		name    string
		runs    []api.WorkflowRunResponse
		want    string
		wantErr string
	}{
		{"one workflow", []api.WorkflowRunResponse{{WorkflowName: "orders"}, {WorkflowName: "orders"}}, "orders", ""},
		{"several workflows", []api.WorkflowRunResponse{{WorkflowName: "orders"}, {WorkflowName: "refunds"}}, "", "orders, refunds"},
		{"no runs", nil, "", "no workflow runs"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/apps/web/workflows/runs" || r.URL.Query().Get("workflow_name") != "" {
					http.NotFound(w, r)
					return
				}
				main.WriteJSONTest(w, api.ListWorkflowRunsResponse{Runs: tt.runs, Total: len(tt.runs)})
			}))
			t.Cleanup(srv.Close)
			got, err := main.InferLatestWorkflowName(context.Background(), api.NewClient(srv.URL, "fp_test_x"), "web")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to mention %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("InferLatestWorkflowName = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}
