package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvironmentGitOpsCLIReviewedActions(t *testing.T) {
	for _, action := range []string{"approve", "adopt"} {
		t.Run(action, func(t *testing.T) {
			resetJSONOut(t)
			f := authedFakeAPI(t, `{}`, http.StatusAccepted)
			file := filepath.Join(t.TempDir(), "review.json")
			body := `{"commit_sha":"` + strings.Repeat("a", 40) + `","definition_digest":"` + strings.Repeat("b", 64) + `","generation":7}`
			if action == "adopt" {
				body = `{"plan_hash":"` + strings.Repeat("c", 64) + `","blocking_reasons":[],"changes":[]}`
			}
			if err := os.WriteFile(file, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			args := []string{action, "shop", "production", "--file", file}
			if cmdProjectsEnvironmentGitOps(args) == 0 || f.sawMethod != "" {
				t.Fatal("reviewed action executed without explicit confirmation")
			}
			if cmdProjectsEnvironmentGitOps(append(args, "--yes")) != 0 {
				t.Fatal("confirmed reviewed action failed")
			}
			path := "/v1/projects/shop/environments/production/gitops/adopt"
			if action == "approve" {
				path = "/v1/projects/shop/environments/production/gitops/revisions/approve"
			}
			if f.sawMethod != http.MethodPost || f.sawPath != path {
				t.Fatalf("request: %s %s", f.sawMethod, f.sawPath)
			}
			var request map[string]any
			if err := json.Unmarshal(f.sawBody, &request); err != nil {
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
	resetJSONOut(t)
	f := authedFakeAPI(t, `{}`, http.StatusOK)
	if code := cmdProjectsEnvironmentGitOps([]string{"controls", "shop", "staging", "--generation", "3", "--prune=false", "--suspended=false"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var request map[string]any
	if err := json.Unmarshal(f.sawBody, &request); err != nil {
		t.Fatal(err)
	}
	if f.sawMethod != http.MethodPatch || request["prune"] != false || request["suspended"] != false || request["expected_generation"] != float64(3) {
		t.Fatalf("controls: %s %+v", f.sawMethod, request)
	}
}

func TestEnvironmentGitOpsCLIRejectsBlockedAdoption(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{}`, http.StatusAccepted)
	file := filepath.Join(t.TempDir(), "blocked.json")
	if err := os.WriteFile(file, []byte(`{"plan_hash":"`+strings.Repeat("a", 64)+`","blocking_reasons":["owned by Terraform"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if cmdProjectsEnvironmentGitOps([]string{"adopt", "shop", "production", "--file", file, "--yes"}) == 0 || f.sawMethod != "" {
		t.Fatal("blocked ownership plan sent to adoption API")
	}
}
