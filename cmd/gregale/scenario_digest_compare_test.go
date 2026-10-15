package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/chaos"
)

func digestComparisonReceipt(id string, passed bool, observed float64) testRunReceipt {
	r := groupingTestReceipt(id, observed, 100, "<=")
	r.Replay.ConfigurationSHA256 = strings.Repeat("a", 64)
	p := &r.Load.ChaosPhases[0]
	p.ActualDurationMS, p.Requests = 1000, 10
	p.Latency = testLoadLatency{Samples: 10, P95MS: observed}
	rule := chaos.Rule{Kind: "tcp_reset", To: "dependency", Percent: 100}
	p.Chaos = &testChaosEvidence{Rules: []chaos.Rule{rule}, Matches: []testChaosRuleEvidence{{Rule: rule, Matches: 3, MinMatches: 1}}}
	if passed {
		r.Status = "passed"
		p.Thresholds[0].Passed = true
	}
	return r
}

func TestDigestComparisonNewAndNoLongerObservedRequireCoverage(t *testing.T) {
	// adr: 984 — absence means improvement only with matching exercised evidence.
	failed, passed := digestComparisonReceipt("failed", false, 200), digestComparisonReceipt("passed", true, 50)
	for _, tc := range []struct {
		name   string
		before []testRunReceipt
		after  []testRunReceipt
		want   string
	}{
		{"new", []testRunReceipt{passed}, []testRunReceipt{failed}, "newly_observed"},
		{"absent now", []testRunReceipt{failed}, []testRunReceipt{passed}, "no_longer_observed"},
		{"missing context", []testRunReceipt{failed}, nil, "not_comparable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := compareTestDigestGroups(tc.before, tc.after)
			if len(rows) != 1 || rows[0].status != tc.want {
				t.Fatalf("got %+v, want %s", rows, tc.want)
			}
		})
	}
}

func TestDigestComparisonCountsAllRootsAndShowsFrequencyChange(t *testing.T) {
	// adr: 984 — sample-size changes retain both denominators and raw worst values.
	old := []testRunReceipt{digestComparisonReceipt("old-fail", false, 200), digestComparisonReceipt("old-pass", true, 50)}
	current := []testRunReceipt{digestComparisonReceipt("new-a", false, 300), digestComparisonReceipt("new-b", false, 250)}
	rows := compareTestDigestGroups(old, current)
	if len(rows) != 1 || rows[0].status != "persisting" || rows[0].before.group.Failures != 1 ||
		rows[0].after.group.Failures != 2 || rows[0].beforeRuns != 2 || rows[0].afterRuns != 2 ||
		rows[0].delta == nil || *rows[0].delta != 50 || rows[0].after.group.WorstObserved == nil ||
		*rows[0].after.group.WorstObserved != 300 {
		t.Fatalf("wrong counts or observations: %+v", rows)
	}
}

func TestDigestComparisonMissingEvidenceCannotResolveFailure(t *testing.T) {
	// adr: 984 — incomplete phases, missed faults and changed policies fail comparability.
	old := []testRunReceipt{digestComparisonReceipt("old", false, 200)}
	for _, kind := range []string{"phase", "assertion", "fault", "configuration", "skipped", "case"} {
		t.Run(kind, func(t *testing.T) {
			current := digestComparisonReceipt("new", true, 50)
			switch kind {
			case "phase":
				current.Load.ChaosPhases = nil
			case "assertion":
				current.Load.ChaosPhases[0].Thresholds = nil
			case "fault":
				current.Load.ChaosPhases[0].Chaos.Matches[0].Matches = 0
			case "configuration":
				current.Replay.ConfigurationSHA256 = strings.Repeat("b", 64)
			case "skipped":
				current.Status = "skipped"
			case "case":
				current.Case = "different-case"
			}
			rows := compareTestDigestGroups(old, []testRunReceipt{current})
			if len(rows) != 1 || rows[0].status != "not_comparable" || rows[0].delta != nil {
				t.Fatalf("incomplete evidence resolved a failure: %+v", rows)
			}
		})
	}
}

func TestDigestComparisonEarlierFailureCannotHideUnpassedAssertion(t *testing.T) {
	// adr: 984 — first-failure grouping cannot prove a later failed assertion disappeared.
	old := digestComparisonReceipt("old", false, 200)
	current := digestComparisonReceipt("new", false, 200)
	p := &current.Load.ChaosPhases[0]
	p.Thresholds = append([]testLoadThresholdEvidence{{Metric: "error_rate", Operator: "<=", Limit: 0, Actual: 1}}, p.Thresholds...)
	rows := compareTestDigestGroups([]testRunReceipt{old}, []testRunReceipt{current})
	for _, row := range rows {
		if row.key.Assertion == "p95_ms" {
			if row.status != "not_comparable" {
				t.Fatal("later failed threshold was reported as no longer observed")
			}
			return
		}
	}
	t.Fatal("baseline group disappeared from the comparison")
}

func TestDigestBaselineCLIProtectsInputsAndRendersLinks(t *testing.T) {
	// adr: 984 — offline baseline reports remain intact and evidence links retain their source.
	dir := t.TempDir()
	oldPath, currentPath := filepath.Join(dir, "old.json"), filepath.Join(dir, "new.json")
	old := []testRunReceipt{digestComparisonReceipt("old", false, 200)}
	current := []testRunReceipt{digestComparisonReceipt("new", false, 250)}
	var oldBody []byte
	for path, rows := range map[string][]testRunReceipt{oldPath: old, currentPath: current} {
		body, err := json.Marshal(rows)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
		if path == oldPath {
			oldBody = body
		}
	}
	if code := cmdTestDigest([]string{"--from", currentPath, "--baseline", oldPath, "--markdown", oldPath}); code == 0 {
		t.Fatal("baseline was allowed as an output destination")
	}
	after, err := os.ReadFile(oldPath)
	if err != nil || !bytes.Equal(oldBody, after) {
		t.Fatal("baseline input changed")
	}
	output := filepath.Join(dir, "digest.md")
	if code := cmdTestDigest([]string{"--from", currentPath, "--baseline", oldPath, "--markdown", output,
		"--evidence-url", "https://example.test/current.html", "--baseline-evidence-url", "https://example.test/old.html"}); code != 0 {
		t.Fatalf("digest comparison returned %d", code)
	}
	body, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"persisting", "200.000 -> 250.000", "current.html#run-evidence-0", "old.html#run-evidence-0", "replay example"} {
		if !strings.Contains(string(body), expected) {
			t.Fatalf("comparison omitted %q", expected)
		}
	}
	if code := cmdTestDigest([]string{"--from", currentPath, "--markdown", output, "--baseline-evidence-url", "https://example.test/old.html"}); code == 0 {
		t.Fatal("baseline evidence URL was accepted without a baseline")
	}
}

func TestDigestConfigurationRecordsPassingAndReducedPolicies(t *testing.T) {
	// adr: 984 — successful runs retain policy identity without replay capability.
	plan := testRunPlan{Prepared: &testPreparedScenario{Name: "checkout"}, Profile: "warm"}
	receipt := testRunReceipt{Status: "passed"}
	recordTestDigestConfiguration(&receipt, plan)
	if len(receipt.ConfigurationSHA256) != 64 || receipt.Replay != nil || receipt.Status != "passed" {
		t.Fatal("passing receipt lost its policy fingerprint or acquired replay state")
	}
	plan.ReplayManifest = &testReplayManifest{ConfigurationSHA256: strings.Repeat("a", 64), ReducedConfigurationSHA256: strings.Repeat("b", 64)}
	recordTestDigestConfiguration(&receipt, plan)
	if receipt.ConfigurationSHA256 != strings.Repeat("b", 64) {
		t.Fatal("reduced policy was labeled with its original fingerprint")
	}
}

func TestDigestComparisonAcceptsPassingFingerprintWithoutReplay(t *testing.T) {
	// adr: 984 — a new successful receipt need not carry a failure replay manifest.
	old := digestComparisonReceipt("old", false, 200)
	current := digestComparisonReceipt("new", true, 50)
	current.ConfigurationSHA256 = current.Replay.ConfigurationSHA256
	current.Replay = nil
	rows := compareTestDigestGroups([]testRunReceipt{old}, []testRunReceipt{current})
	if len(rows) != 1 || rows[0].status != "no_longer_observed" {
		t.Fatal("a passing configuration fingerprint was ignored")
	}
}
