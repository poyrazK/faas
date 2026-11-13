// adr: 1303
package dashboard_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/storedworkflowgate"
)

func TestBaselineFormsKeepEditorDataPrivateAndEscapeEvidence(t *testing.T) {
	request := api.PromoteStoredWorkflowLiveBaselineRequest{SuiteID: "suite", Policy: json.RawMessage(`{"version":2,"required_cases":["<script>unsafe</script>"],"max_candidate_age_seconds":3600}`)}
	receipt := api.StoredWorkflowLiveBaseline{ActorAccountID: "<script>actor</script>", Policy: request.Policy}
	data := dashboard.StoredWorkflowConsoleData{Name: "report", Version: 3, Definition: "PRIVATE-DRAFT", Sample: "PRIVATE-SAMPLE", BaseURL: "/dashboard/apps/demo/workflows/stored", LiveSuite: &api.StoredWorkflowLiveSuite{StoredWorkflowLiveSuiteSummary: api.StoredWorkflowLiveSuiteSummary{ID: "suite"}}, Baseline: &dashboard.StoredWorkflowBaselineForm{Name: "production", Ready: true, Head: &receipt, Policy: string(request.Policy), Request: &request, PromotionID: "stable-id", Preview: &storedworkflowgate.Result{Outcome: "passed"}}}

	age := 3601.0
	data.BaselineCatalog = &api.ListStoredWorkflowLiveBaselinesResponse{ObservedAt: time.Now().UTC(), Baselines: []api.StoredWorkflowLiveBaselineHealth{{Baseline: api.StoredWorkflowLiveBaseline{BaselineName: "staging", Revision: 2, Policy: request.Policy}, EvidenceStatus: "usable", RenewalStatus: "inconclusive", AgeSeconds: &age, Reasons: []string{"candidate_evidence_stale"}, Cases: []api.StoredWorkflowLiveBaselineCaseHealth{{Name: "<script>unsafe</script>", RunID: "retained-run", Status: "passed"}}}}}
	w := httptest.NewRecorder()
	if err := dashboard.Render(w, slog.New(slog.NewTextHandler(io.Discard, nil)), "nonce", dashboard.Page{Body: "stored_workflows", Data: data}); err != nil {
		t.Fatal(err)
	}
	body := w.Body.String()
	start := strings.Index(body, `<section id="stored-workflow-baselines">`)
	if start < 0 || start < strings.Index(body, "</form>") {
		t.Fatal("baseline controls nested inside editor")
	}
	section := body[start:]
	if strings.Contains(section, "PRIVATE-DRAFT") || strings.Contains(section, "PRIVATE-SAMPLE") || strings.Contains(section, "<script>unsafe") || strings.Contains(section, "<script>actor") {
		t.Fatal("private editor or unescaped evidence in baseline controls")
	}

	for _, required := range []string{"Passing reports retained", "staging", "3601", "oldest run exceeds", "verification=retained-run", "Prepare replacement suite", "do not expire a baseline for CI"} {
		if !strings.Contains(section, required) {
			t.Fatal("missing catalog detail", required)
		}
	}
	readForm := section[:strings.Index(section, "</form>")]
	if strings.Contains(readForm, "csrf_token") || strings.Contains(readForm, "baseline_policy") || strings.Contains(readForm, "promotion_id") {
		t.Fatal("GET leaks promotion intent")
	}
	if !strings.Contains(section, `name="promotion_id" value="stable-id"`) || !strings.Contains(section, `name="confirm_baseline"`) {
		t.Fatal("missing explicit promotion controls")
	}
}
