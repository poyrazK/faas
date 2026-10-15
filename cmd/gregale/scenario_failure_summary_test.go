package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/chaos"
)

func TestFailureSummarySelectsFirstAssertionAndRelevantFault(t *testing.T) {
	// adr: 984 — selected evidence must belong to the failed campaign phase.
	load := &testLoadEvidence{ChaosPhases: []testLoadChaosPhaseEvidence{
		{Name: "baseline", Role: "baseline"},
		{Name: "fault", Role: "fault", Chaos: &testChaosEvidence{Matches: []testChaosRuleEvidence{
			{Rule: chaos.Rule{Kind: chaos.KindTCPReset, To: "payment"}, Matches: 3, MinMatches: 1},
		}}},
		{Name: "recovery", Role: "recovery", Recovery: &testLoadRecoveryEvidence{Error: "p95_ms <= 100, observed 200"}},
	}}
	r := testRunReceipt{Status: "failed", Scenario: "checkout", Steps: []testScenarioStepEvidence{
		{Name: "campaign", Status: "failed", Load: load},
		{Name: "unexecuted", Status: "skipped", Error: "not a failure"},
	}}
	summary := r.FailureSummary()
	if summary.Scope != "checkout / campaign / recovery" || !strings.Contains(summary.Failure, "observed 200") ||
		len(summary.Matches) != 1 || !strings.Contains(summary.Matches[0], "3 matches") {
		t.Fatalf("wrong first failure or match evidence: %+v", summary)
	}
	load.ChaosPhases[1].Chaos = nil
	r.Chaos = &testChaosEvidence{Matches: []testChaosRuleEvidence{{Rule: chaos.Rule{To: "also-unrelated"}, Matches: 100}}}
	r.Steps[0].Chaos = &testChaosEvidence{Matches: []testChaosRuleEvidence{{Rule: chaos.Rule{To: "unrelated"}, Matches: 99}}}
	if len(r.FailureSummary().Matches) != 0 {
		t.Fatal("unrelated step matches substituted for missing phase evidence")
	}
}

func TestFailureSummaryPreservesGroupAndCleanupErrors(t *testing.T) {
	// adr: 984 — tolerated child failures do not fail passing groups.
	child := testRunReceipt{Scenario: "child", Status: "failed", Error: "assertion failed", CleanupError: "session remains"}
	r := testRunReceipt{Scenario: "group", Status: "passed", Resilience: &testResilienceEvidence{History: []testRunReceipt{child}}}
	if r.FailureSummary() != nil {
		t.Fatal("passing group acquired a failure summary")
	}
	r.Status, r.Error, r.CleanupError = "failed", "pass-rate budget failed", "namespace remains"
	summary := r.FailureSummary()
	if summary.GroupError != r.Error || summary.Failure != child.Error || len(summary.Cleanup) != 2 {
		t.Fatalf("group or cleanup evidence lost: %+v", summary)
	}
}

func TestFailureSummaryReplayQuotesSelectorAndRejectsUnsupportedReceipt(t *testing.T) {
	// adr: 984 — replay uses a stored isolated selector and never shell code.
	r := testRunReceipt{Scenario: "checkout", Profile: "warm", Status: "failed", Engine: "real-vm", RunID: "run'$(unsafe)",
		Replay: &testReplayManifest{Version: 1, Scenario: "checkout", Profile: "warm"}}
	command := r.FailureSummary().Replay
	if !strings.Contains(command, "--run-id 'run'\"'\"'$(unsafe)'") || !strings.Contains(command, "'<report.json>'") {
		t.Fatalf("selector was not shell-quoted: %s", command)
	}
	r.Replay.Version = 99
	if r.FailureSummary().Replay != "" {
		t.Fatal("unsupported replay metadata produced a command")
	}
}

func TestFailureSummaryHTMLLinksEvidenceAndEscapesContent(t *testing.T) {
	// adr: 984 — descriptive views preserve the failed receipt and escape its data.
	r := testRunReceipt{Scenario: "<script>bad</script>", Status: "failed", Error: "<img src=x onerror=unsafe>"}
	path := filepath.Join(t.TempDir(), "failure.html")
	if err := writeTestHTMLReport(path, "", []testRunReceipt{r, r}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	for _, expected := range []string{`href="#run-evidence-0"`, `id="run-evidence-0"`, `href="#run-evidence-1"`, `id="run-evidence-1"`, "Injection is unconfirmed", "No recovery observation recorded", "&lt;script&gt;bad&lt;/script&gt;"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("failure overview omitted %q", expected)
		}
	}
	if strings.Contains(html, "<script>bad</script>") || strings.Contains(html, "<img src=x") {
		t.Fatal("failure summary emitted executable HTML")
	}
	if r.Status != "failed" || r.Error != "<img src=x onerror=unsafe>" {
		t.Fatal("rendering changed the receipt")
	}
}
