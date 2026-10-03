package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDeploymentAliasClientCRUD(t *testing.T) {
	var gotMethods, gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethods = append(gotMethods, r.Method)
		gotPaths = append(gotPaths, r.URL.Path)
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(DeploymentAliasListResponse{Items: []DeploymentAliasResponse{{Name: "candidate", Revision: 7}}})
		case http.MethodPut:
			var req SetDeploymentAliasRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode request: %v", err)
			}
			if req.DeploymentID != "0123456789abcdef0123456789abcdef" {
				t.Errorf("deployment_id = %q", req.DeploymentID)
			}
			_ = json.NewEncoder(w).Encode(DeploymentAliasResponse{Name: "candidate", DeploymentID: req.DeploymentID, Revision: 7})
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "fp_test")
	ctx := context.Background()
	list, err := client.ListDeploymentAliases(ctx, "demo app")
	if err != nil || len(list.Items) != 1 || list.Items[0].Revision != 7 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	set, err := client.SetDeploymentAlias(ctx, "demo app", "candidate", SetDeploymentAliasRequest{DeploymentID: "0123456789abcdef0123456789abcdef"})
	if err != nil || set.Name != "candidate" || set.Revision != 7 {
		t.Fatalf("set = %+v, %v", set, err)
	}
	if err := client.DeleteDeploymentAlias(ctx, "demo app", "candidate"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	wantMethods := []string{http.MethodGet, http.MethodPut, http.MethodDelete}
	wantPaths := []string{
		"/v1/apps/demo app/deployment-aliases",
		"/v1/apps/demo app/deployment-aliases/candidate",
		"/v1/apps/demo app/deployment-aliases/candidate",
	}
	for i := range wantMethods {
		if gotMethods[i] != wantMethods[i] || gotPaths[i] != wantPaths[i] {
			t.Errorf("request[%d] = %s %q, want %s %q", i, gotMethods[i], gotPaths[i], wantMethods[i], wantPaths[i])
		}
	}
}
