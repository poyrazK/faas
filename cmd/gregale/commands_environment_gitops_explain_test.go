package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestEnvironmentGitOpsExplainIsReadOnlyAndSupportsOlderStatus(t *testing.T) {
	for _, mode := range []string{"text", "json-flag", "global-json"} {
		t.Run(mode, func(t *testing.T) {
			resetJSONOut(t)
			if mode == "global-json" {
				jsonOutput = true
			}
			status := api.EnvironmentGitOpsStatusResponse{Source: api.EnvironmentGitSource{ID: "source", ApprovedRevisionID: "approved", AppliedRevisionID: "historical", Spec: api.EnvironmentGitSourceSpec{Mode: "report"}}}
			body, err := json.Marshal(status)
			if err != nil {
				t.Fatal(err)
			}
			fake := authedFakeAPI(t, string(body), http.StatusOK)
			args := []string{"environments", "gitops", "explain", "shop", "production"}
			if mode == "json-flag" {
				args = append(args, "--json")
			}
			var out bytes.Buffer
			code := captureStdoutSwap(t, &out, func() int { return cmdProjects(args) })
			if code != 0 || fake.sawMethod != http.MethodGet || fake.sawPath != "/v1/projects/shop/environments/production/gitops" {
				t.Fatalf("request=%s %s code=%d", fake.sawMethod, fake.sawPath, code)
			}
			if mode != "text" {
				var result api.EnvironmentGitOpsExplanation
				if err := json.Unmarshal(out.Bytes(), &result); err != nil || !result.ReadOnly || result.Status != "reporting" || result.ServingRevisionID != "" {
					t.Fatalf("unsafe explanation: %s %v", out.String(), err)
				}
			} else if !strings.Contains(out.String(), "Serving revision: unknown") || !strings.Contains(out.String(), "does not apply changes") {
				t.Fatalf("missing guidance: %s", out.String())
			}
		})
	}
}

func TestEnvironmentGitOpsExplainRejectsInvalidTargetBeforeRequest(t *testing.T) {
	resetJSONOut(t)
	fake := authedFakeAPI(t, `{}`, http.StatusOK)
	if code := cmdProjectsEnvironmentGitOps([]string{"explain", "../other", "production"}); code == 0 || fake.sawMethod != "" {
		t.Fatal("invalid target reached API")
	}
}

func TestEnvironmentGitOpsExplainEscapesTerminalControlCharacters(t *testing.T) {
	var out bytes.Buffer
	if err := writeEnvironmentGitOpsExplanation(&out, api.EnvironmentGitOpsExplanation{Summary: "safe\n\x1b[2J", Issues: []api.EnvironmentGitOpsDiagnostic{{Action: "inspect\routput"}}}); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(out.String(), "\x1b\r") {
		t.Fatalf("terminal control leaked: %q", out.String())
	}
}

func TestEnvironmentGitOpsExplainCacheHitKeepsQualificationPending(t *testing.T) {
	var out bytes.Buffer
	err := writeEnvironmentGitOpsExplanation(&out, api.EnvironmentGitOpsExplanation{Workloads: []api.EnvironmentWorkloadProgress{{Resource: "workload/api", BuildStatus: "succeeded", BuildCacheStatus: "hit", Preparation: "complete", Qualification: "pending", Activation: "pending", Serving: "unknown"}}})
	if err != nil || !strings.Contains(out.String(), "Build cache: hit (fresh qualification remains required)") || !strings.Contains(out.String(), "qualification pending; activation pending; serving unknown") {
		t.Fatalf("cache status changed qualification: %s %v", out.String(), err)
	}
}
