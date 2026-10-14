// adr: 1155 — candidate assessment and customer status feedback contracts.
package dashboard

import (
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestGitOpsDashboardSupportsGitLabBindingAndProviderControls(t *testing.T) {
	for _, bound := range []bool{false, true} {
		data := EnvironmentGitOpsData{Project: "shop", Environment: "staging", CSRFToken: "csrf"}
		if bound {
			data.Status = &api.EnvironmentGitOpsStatusResponse{Source: api.EnvironmentGitSource{Spec: api.EnvironmentGitSourceSpec{Provider: "gitlab", Mode: "report", ApprovalPolicy: "manual"}}}
		}
		rec := httptest.NewRecorder()
		if err := Render(rec, slog.Default(), "", Page{Title: "GitOps", Body: "environment_gitops", Data: data}); err != nil {
			t.Fatal(err)
		}
		body := rec.Body.String()
		if !bound {
			for _, want := range []string{`name="provider" value="gitlab"`, `name="repository"`, `name="ref" value="refs/heads/main"`, `name="approval_policy" value="manual"`} {
				if !strings.Contains(body, want) {
					t.Fatalf("GitLab binding missing %s", want)
				}
			}
		} else if strings.Contains(body, `value="protected_branch"`) || strings.Contains(body, `name="prune"`) || !strings.Contains(body, "GitLab.com uses manual approval") {
			t.Fatalf("GitLab exposed unsupported controls: %s", body)
		}
	}
}

func TestGitOpsDashboardCandidatePlanDisplaysPinnedObservation(t *testing.T) {
	data := EnvironmentGitOpsData{Project: "shop", Environment: "staging", Status: &api.EnvironmentGitOpsStatusResponse{}, PlanPreview: &api.PreviewEnvironmentGitPlanResponse{
		Plan: api.EnvironmentGitOpsPlan{CommitSHA: strings.Repeat("a", 40), Generation: 3, ObservedVersion: 7, BlockingReasons: []string{"ownership conflict"}, Changes: []api.EnvironmentGitOpsChange{{Resource: "workload/api", Path: "variables/MODE", Action: "conflict"}}}}}
	rec := httptest.NewRecorder()
	if err := Render(rec, slog.Default(), "", Page{Title: "GitOps", Body: "environment_gitops", Data: data}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Candidate plan", "source generation 3", "observed intent 7", "Values are redacted", "variables/MODE", "ownership conflict"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("candidate plan missing %s", want)
		}
	}
}
