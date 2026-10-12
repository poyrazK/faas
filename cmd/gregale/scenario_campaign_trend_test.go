package main

import (
	"bytes"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCampaignCoverageTrendRawHistoryAndFindings(t *testing.T) {
	s, baseline, path := campaignRegressionFixture(t)
	s.Resilience.CampaignCoverage.Regression = nil
	current := runCampaignRequirementsTest(t, s, true, 0)
	// Independent executions must have distinct leaf identities.
	current.RunID = "trend-current"
	for i := range current.FaultMatrix.Cases {
		for j := range current.FaultMatrix.Cases[i].Resilience.History {
			leaf := &current.FaultMatrix.Cases[i].Resilience.History[j]
			leaf.RunID = "trend-" + leaf.RunID
			current.FaultMatrix.CampaignCoverage.Seeds[i].Runs[j].RunID = leaf.RunID
		}
	}
	next := filepath.Join(t.TempDir(), "next.json")
	if err := writeTestReport(next, []testRunReceipt{current}); err != nil {
		t.Fatal(err)
	}
	cfg := testCampaignTrendConfig{Reports: []testCampaignTrendInput{{FromReport: path, RunID: baseline.RunID, Label: "<script>bad</script>"}, {FromReport: next, RunID: current.RunID, Label: "v2"}}, MaxDropPercentagePoints: map[int]float64{0: 0}, MinPlannedEvents: 2}
	s, err := expandTestFaultSweep(s)
	if err != nil {
		t.Fatal(err)
	}
	trend, err := buildTestCampaignTrend(s, "checkout", "", cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(trend.Points) != 2 || !trend.Points[1].Checks[0].LostCoverage || trend.Points[1].Checks[0].DropStatus != "failed" || len(trend.Points[0].ReportSHA256) != 64 {
		t.Fatal("missing loss/tolerance/provenance", trend)
	}
	var html bytes.Buffer
	if err = campaignTrendTemplate.Execute(&html, trend); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html.String(), "<script>") || !strings.Contains(html.String(), "href=\"#point-2\"") || !strings.Contains(html.String(), "<line") {
		t.Fatal("unsafe or missing chart")
	}
	if _, err = buildTestCampaignTrend(s, "checkout", "", cfg, path); err == nil {
		t.Fatal("input overwrite allowed")
	}
	cfg.Reports[1] = cfg.Reports[0]
	if _, err = buildTestCampaignTrend(s, "checkout", "", cfg, ""); err == nil {
		t.Fatal("duplicate execution allowed")
	}
}

func TestCampaignCoverageTrendRejectsInvalidInputs(t *testing.T) {
	s, baseline, path := campaignRegressionFixture(t)
	s.Resilience.CampaignCoverage.Regression = nil
	for _, cfg := range []testCampaignTrendConfig{{}, {Reports: []testCampaignTrendInput{{FromReport: path, RunID: baseline.RunID}}}, {MinExercisedRate: 2, Reports: make([]testCampaignTrendInput, 2)}} {
		if _, err := buildTestCampaignTrend(s, "checkout", "", cfg, ""); err == nil {
			t.Fatal("invalid config accepted")
		}
	}
	config := filepath.Join(t.TempDir(), "trend.yaml")
	if err := os.WriteFile(config, []byte("reports: []\nunknown: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if cmdTest([]string{"coverage-trend", "--scenario", "checkout", "--config", config, "--html", filepath.Join(t.TempDir(), "out.html")}) == 0 {
		t.Fatal("unknown configuration field accepted")
	}
}

func TestCampaignCoverageTrendOfflineCLI(t *testing.T) {
	t.Setenv("FAAS_TOKEN", "")
	dir := t.TempDir()
	for name, body := range map[string]string{"package.json": `{"scripts":{"start":"node server.js"}}`, "server.js": "console.log('fixture');"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	manifest := filepath.Join(dir, "gregale-test.yaml")
	source := campaignCoverageScenario()
	source.Resilience.CampaignCoverage.MinExercised = map[int]int{0: 1, 1: 1}
	body, err := yaml.Marshal(testManifest{Version: 1, Scenarios: map[string]testScenario{"checkout": source}})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(manifest, body, 0600); err != nil {
		t.Fatal(err)
	}
	scenarios, _, err := readTestManifestForScenario(manifest, "checkout", nil)
	if err != nil {
		t.Fatal(err)
	}
	a := runCampaignRequirementsTest(t, scenarios["checkout"], false, 0)
	b := runCampaignRequirementsTest(t, scenarios["checkout"], false, 0)
	for i := range b.FaultMatrix.Cases {
		for j := range b.FaultMatrix.Cases[i].Resilience.History {
			leaf := &b.FaultMatrix.Cases[i].Resilience.History[j]
			leaf.RunID = "second-" + leaf.RunID
			b.FaultMatrix.CampaignCoverage.Seeds[i].Runs[j].RunID = leaf.RunID
		}
	}
	report := filepath.Join(dir, "runs.json")
	if err = writeTestReport(report, []testRunReceipt{a, b}); err != nil {
		t.Fatal(err)
	}
	cfg := testCampaignTrendConfig{Reports: []testCampaignTrendInput{{FromReport: "runs.json", RunID: a.RunID, Label: "v1"}, {FromReport: "runs.json", RunID: b.RunID, Label: "v2"}}}
	config := filepath.Join(dir, "trend.yaml")
	body, err = yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(config, body, 0600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "trend.html")
	args := []string{"coverage-trend", "--manifest", manifest, "--scenario", "checkout", "--config", config, "--html", output}
	if cmdTest(args) != 0 {
		t.Fatal("offline trend failed")
	}
	rendered, err := os.ReadFile(output)
	if err != nil || !bytes.Contains(rendered, []byte(a.RunID)) || !bytes.Contains(rendered, []byte("v2")) {
		t.Fatal("missing trend output", err)
	}
	alias := filepath.Join(dir, "alias.html")
	if err = os.Link(report, alias); err != nil {
		t.Fatal(err)
	}
	args[len(args)-1] = alias
	if cmdTest(args) == 0 {
		t.Fatal("hardlink overwrite permitted")
	}
	// Raw summaries cannot override the validated leaf history.
	b.FaultMatrix.CampaignCoverage.Choices[0].Exercised++
	if err = writeTestReport(report, []testRunReceipt{a, b}); err != nil {
		t.Fatal(err)
	}
	args[len(args)-1] = output
	if cmdTest(args) == 0 {
		t.Fatal("corrupted coverage summary accepted")
	}
}
