package main_test

import (
	"encoding/json"
	"github.com/onebox-faas/faas/cmd/gregale"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvironmentGitOpsCLIReviewedActions(t *testing.T) {
	for _, action := range []string{"approve", "adopt"} {
		t.Run(action, func(t *testing.T) {
			main.ResetJSONOut(t)
			f := main.AuthedFakeAPI(t, `{}`, http.StatusAccepted)
			file := filepath.Join(t.TempDir(), "review.json")
			body := `{"commit_sha":"` + strings.Repeat("a", 40) + `","definition_digest":"` + strings.Repeat("b", 64) + `","generation":7}`
			if action == "adopt" {
				body = `{"plan_hash":"` + strings.Repeat("c", 64) + `","blocking_reasons":[],"changes":[]}`
			}
			if err := os.WriteFile(file, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			args := []string{action, "shop", "production", "--file", file}
			if main.CmdProjectsEnvironmentGitOps(args) == 0 || (*f.SawMethod()) != "" {
				t.Fatal("reviewed action executed without explicit confirmation")
			}
			if main.CmdProjectsEnvironmentGitOps(append(args, "--yes")) != 0 {
				t.Fatal("confirmed reviewed action failed")
			}
			path := "/v1/projects/shop/environments/production/gitops/adopt"
			if action == "approve" {
				path = "/v1/projects/shop/environments/production/gitops/revisions/approve"
			}
			if (*f.SawMethod()) != http.MethodPost || (*f.SawPath()) != path {
				t.Fatalf("request: %s %s", (*f.SawMethod()), (*f.SawPath()))
			}
			var request map[string]any
			if err := json.Unmarshal((*f.SawBody()), &request); err != nil {
				t.Fatal(err)
			}
			if action == "approve" && (request["expected_generation"] != float64(7) || request["definition_digest"] != strings.Repeat("b", 64)) {
				t.Fatalf("review receipt was not preserved: %+v", request)
			}
			if action == "adopt" && request["plan_hash"] != strings.Repeat("c", 64) {
				t.Fatalf("adoption receipt was not preserved: %+v", request)
			}
		})
	}
}

func TestEnvironmentGitOpsCLIControlsPreserveExplicitFalse(t *testing.T) {
	main.ResetJSONOut(t)
	f := main.AuthedFakeAPI(t, `{}`, http.StatusOK)
	if code := main.CmdProjectsEnvironmentGitOps([]string{"controls", "shop", "staging", "--generation", "3", "--prune=false", "--suspended=false"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var request map[string]any
	if err := json.Unmarshal((*f.SawBody()), &request); err != nil {
		t.Fatal(err)
	}
	if (*f.SawMethod()) != http.MethodPatch || request["prune"] != false || request["suspended"] != false || request["expected_generation"] != float64(3) {
		t.Fatalf("controls: %s %+v", (*f.SawMethod()), request)
	}
}

func TestEnvironmentGitOpsCLIRejectsBlockedAdoption(t *testing.T) {
	main.ResetJSONOut(t)
	f := main.AuthedFakeAPI(t, `{}`, http.StatusAccepted)
	file := filepath.Join(t.TempDir(), "blocked.json")
	if err := os.WriteFile(file, []byte(`{"plan_hash":"`+strings.Repeat("a", 64)+`","blocking_reasons":["owned by Terraform"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if main.CmdProjectsEnvironmentGitOps([]string{"adopt", "shop", "production", "--file", file, "--yes"}) == 0 || (*f.SawMethod()) != "" {
		t.Fatal("blocked ownership plan sent to adoption API")
	}
}

func TestEnvironmentGitOpsCLIProtectedBinding(t *testing.T) {
	main.ResetJSONOut(t)
	f := main.AuthedFakeAPI(t, `{}`, http.StatusCreated)
	if code := main.CmdProjectsEnvironmentGitOps([]string{"bind", "shop", "production", "--manifest-path", "environments/production.yaml", "--approval-policy", "protected_branch"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var req map[string]any
	if err := json.Unmarshal((*f.SawBody()), &req); err != nil {
		t.Fatal(err)
	}
	if req["approval_policy"] != "protected_branch" || req["mode"] != "report" {
		t.Fatalf("binding: %+v", req)
	}
}

func TestEnvironmentGitOpsCLIBindingLifecycleRequiresReviewedGeneration(t *testing.T) {
	for _, action := range []string{"rebind", "unbind"} {
		t.Run(action, func(t *testing.T) {
			main.ResetJSONOut(t)
			status := http.StatusCreated
			method := http.MethodPost
			if action == "unbind" {
				status = http.StatusNoContent
				method = http.MethodDelete
			}
			f := main.AuthedFakeAPI(t, `{}`, status)
			args := []string{action, "shop", "production", "--expected-generation", "7"}
			if action == "rebind" {
				args = append(args, "--manifest-path", "env/new.yaml", "--ref", "refs/heads/reviewed")
			}
			if main.CmdProjectsEnvironmentGitOps(args) == 0 || (*f.SawMethod()) != "" {
				t.Fatal("lifecycle changed ownership without confirmation")
			}
			if main.CmdProjectsEnvironmentGitOps(append(args, "--yes")) != 0 {
				t.Fatal("reviewed lifecycle request failed")
			}
			want := "/v1/projects/shop/environments/production/gitops/source"
			if action == "rebind" {
				want += "/rebind"
			}
			if (*f.SawMethod()) != method || (*f.SawPath()) != want {
				t.Fatalf("request: %s %s", (*f.SawMethod()), (*f.SawPath()))
			}
			var body map[string]any
			if err := json.Unmarshal((*f.SawBody()), &body); err != nil || body["expected_generation"] != float64(7) {
				t.Fatalf("generation: %s %v", (*f.SawBody()), err)
			}
		})
	}
}
