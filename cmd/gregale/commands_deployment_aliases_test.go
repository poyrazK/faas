package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdDeploymentAliasesListShowsStableURL(t *testing.T) {
	resetJSONOutput()
	t.Cleanup(resetJSONOutput)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/demo/deployment-aliases" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(api.DeploymentAliasListResponse{Items: []api.DeploymentAliasResponse{{
			Name: "candidate", DeploymentID: "0123456789abcdef0123456789abcdef", Revision: 7,
			URL: "https://tag-candidate-demo.gregale.dev",
		}}})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	t.Cleanup(func() { osStdout = oldOut })

	if code := cmdDeployments([]string{"alias", "list", "--app", "demo"}); code != 0 {
		t.Fatalf("deployments alias list = %d", code)
	}
	for _, want := range []string{"candidate", "v7", "https://tag-candidate-demo.gregale.dev"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("list output %q does not contain %q", stdout.String(), want)
		}
	}
}

func TestCmdDeploymentAliasSetResolvesRevision(t *testing.T) {
	resetJSONOutput()
	t.Cleanup(resetJSONOutput)
	const deploymentID = "0123456789abcdef0123456789abcdef"
	var sawPut bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/demo/deployments":
			_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{Items: []api.DeploymentResponse{{ID: deploymentID, Revision: 7}}})
		case r.Method == http.MethodPut && r.URL.Path == "/v1/apps/demo/deployment-aliases/candidate":
			var req api.SetDeploymentAliasRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode request: %v", err)
			}
			if req.DeploymentID != deploymentID {
				t.Errorf("deployment_id = %q, want %q", req.DeploymentID, deploymentID)
			}
			sawPut = true
			_ = json.NewEncoder(w).Encode(api.DeploymentAliasResponse{
				Name: "candidate", DeploymentID: deploymentID, Revision: 7,
				URL: "https://tag-candidate-demo.gregale.dev",
			})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	t.Cleanup(func() { osStdout = oldOut })

	args := []string{"--app", "demo", "--name", "candidate", "--deployment", "v7"}
	if code := cmdDeploymentAliasSet(args); code != 0 {
		t.Fatalf("set = %d", code)
	}
	if !sawPut || !strings.Contains(stdout.String(), "https://tag-candidate-demo.gregale.dev") {
		t.Fatalf("sawPut=%v output=%q", sawPut, stdout.String())
	}
}

func TestCmdDeploymentAliasDeleteJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/v1/apps/demo/deployment-aliases/candidate" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	resetJSONOutput()
	jsonOutput = true
	t.Cleanup(resetJSONOutput)
	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	t.Cleanup(func() { osStdout = oldOut })

	if code := cmdDeploymentAliasDelete([]string{"--app", "demo", "--name", "candidate"}); code != 0 {
		t.Fatalf("delete = %d", code)
	}
	if !strings.Contains(stdout.String(), `"deleted": true`) || !strings.Contains(stdout.String(), `"name": "candidate"`) {
		t.Fatalf("delete JSON = %q", stdout.String())
	}
}

func TestCmdDeploymentAliasSetRejectsInvalidNameBeforeRequest(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	if code := cmdDeploymentAliasSet([]string{"--app", "demo", "--name", "Bad_Name", "--deployment", "v7"}); code == 0 {
		t.Fatal("invalid alias name unexpectedly succeeded")
	}
	if called {
		t.Fatal("invalid alias name made an API request")
	}
}
