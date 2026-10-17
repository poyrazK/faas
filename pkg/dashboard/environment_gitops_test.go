// adr: 1155 — candidate assessment and customer status feedback contracts.
package dashboard

import (
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops/diagnostics"
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
			for _, want := range []string{`name="provider" value="gitlab"`, `name="repository"`, `name="ref" value="refs/heads/main"`, `<option value="manual">`} {
				if !strings.Contains(body, want) {
					t.Fatalf("GitLab binding missing %s", want)
				}
			}
		} else if !strings.Contains(body, `value="protected_branch"`) || !strings.Contains(body, `name="prune"`) || !strings.Contains(body, "Continuous enforcement is unavailable") {
			t.Fatalf("GitLab trust controls or production gate missing: %s", body)
		}
	}
}

func TestGitOpsDashboardExplainsReportModeWithoutClaimingServing(t *testing.T) {
	status := api.EnvironmentGitOpsStatusResponse{Source: api.EnvironmentGitSource{ID: "source", ApprovedRevisionID: "approved", AppliedRevisionID: "historical", Spec: api.EnvironmentGitSourceSpec{Mode: "report"}}}
	explanation := diagnostics.Assess(status, time.Now().UTC())
	status.Explanation = &explanation
	explanation.Issues = append(explanation.Issues, api.EnvironmentGitOpsDiagnostic{Message: "<script>untrusted</script>", Actor: "operator", Action: "Inspect the recorded attempt."})
	rec := httptest.NewRecorder()
	if err := Render(rec, slog.Default(), "", Page{Title: "GitOps", Body: "environment_gitops", Data: EnvironmentGitOpsData{Project: "shop", Environment: "production", Status: &status}}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Why this revision is reporting", "does not apply changes", "Serving revision:", "unknown", "Inspect the recorded attempt."} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("missing diagnostic %q", want)
		}
	}
	if strings.Contains(rec.Body.String(), "<script>untrusted") {
		t.Fatal("diagnostic bypassed HTML escaping")
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

// adr: 1155 — browser guidance stays readable while API diagnostics retain stable codes.
func TestGitOpsDashboardSourceOutageShowsGuidanceWithoutWireCodes(t *testing.T) {
	now := time.Now().UTC()
	status := api.EnvironmentGitOpsStatusResponse{Source: api.EnvironmentGitSource{
		Spec:            api.EnvironmentGitSourceSpec{Provider: "github", Mode: "report", ApprovalPolicy: "manual"},
		SourceCheckedAt: &now, SourceErrorCode: "environment_git_source_unavailable",
	}}
	explanation := diagnostics.Assess(status, now)
	status.Explanation = &explanation
	rec := httptest.NewRecorder()
	if err := Render(rec, slog.Default(), "", Page{Title: "GitOps", Body: "environment_gitops", Data: EnvironmentGitOpsData{Project: "shop", Environment: "production", Status: &status}}); err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	for _, want := range []string{"Git source or merge evidence could not be verified.", "Check provider connectivity and the account connection", "The last approved definition remains authoritative"} {
		if !strings.Contains(body, want) {
			t.Fatalf("source outage guidance missing %q", want)
		}
	}
	if strings.Contains(body, status.Source.SourceErrorCode) {
		t.Fatal("dashboard rendered the source error wire code")
	}
}
