// adr: 1155 — candidate assessment and customer status feedback contracts.
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestGitOpsPlanCLIOutputsBlockedResultBeforeExit(t *testing.T) {
	for _, format := range []string{"json", "markdown"} {
		t.Run(format, func(t *testing.T) {
			resetJSONOut(t)
			sha := strings.Repeat("a", 40)
			preview := api.PreviewEnvironmentGitPlanResponse{SourceID: "source", Project: "shop", Environment: "staging", ReadOnly: true, ValuesRedacted: true,
				Plan: api.EnvironmentGitOpsPlan{Manager: "source", Revision: "candidate:" + sha, CommitSHA: sha, Hash: strings.Repeat("b", 64), DesiredDigest: strings.Repeat("c", 64),
					Changes: []api.EnvironmentGitOpsChange{{Resource: "workload/api", Path: "variables/MODE", Action: "conflict"}}, BlockingReasons: []string{"ownership conflict"}}}
			body, _ := json.Marshal(preview)
			f := authedFakeAPI(t, string(body), http.StatusOK)
			var out bytes.Buffer
			code := captureStdoutSwap(t, &out, func() int {
				return cmdProjectsEnvironmentGitOps([]string{"plan", "shop", "staging", "--commit", sha, "--format", format, "--fail-on-blocked"})
			})
			if code != 2 || f.sawMethod != http.MethodPost || f.sawPath != "/v1/projects/shop/environments/staging/gitops/plan" || !strings.Contains(out.String(), "variables/MODE") {
				t.Fatalf("code=%d request=%s %s output=%s", code, f.sawMethod, f.sawPath, out.String())
			}
		})
	}
}

func TestGitOpsPlanCLIRejectsUnredactedOrWrongCommitResponses(t *testing.T) {
	resetJSONOut(t)
	sha := strings.Repeat("a", 40)
	f := authedFakeAPI(t, `{"read_only":true,"values_redacted":false,"plan":{"changes":[{"before":"PRIVATE"}]}}`, http.StatusOK)
	var out bytes.Buffer
	if code := captureStdoutSwap(t, &out, func() int { return cmdProjectsEnvironmentGitOps([]string{"plan", "shop", "staging", "--commit", sha}) }); code == 0 || strings.Contains(out.String(), "PRIVATE") || f.sawPath == "" {
		t.Fatalf("unsafe response printed: code=%d body=%s", code, out.String())
	}
}

func TestGitOpsPlanMarkdownEscapesProviderControlledText(t *testing.T) {
	var out bytes.Buffer
	if err := writeGitOpsPlanMarkdown(&out, api.PreviewEnvironmentGitPlanResponse{Project: "<script>", Plan: api.EnvironmentGitOpsPlan{BlockingReasons: []string{"bad | field\n`value`"}}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "<script>") || strings.Contains(out.String(), "\n`value`") || !strings.Contains(out.String(), "\\| field") {
		t.Fatalf("unsafe Markdown: %s", out.String())
	}
}
