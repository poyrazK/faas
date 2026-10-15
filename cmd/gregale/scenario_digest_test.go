package main

import (
	"bytes"
	"encoding/json"
	"html"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDigestSeparatesCleanupAndEscapesSavedContent(t *testing.T) {
	// adr: 984 — derived CI output preserves tolerated failures and independent cleanup.
	a := groupingTestReceipt("[run](javascript:unsafe)<script>", 200, 100, "<=")
	a.CleanupError = "namespace remains"
	passed := groupingTestReceipt("healthy", 10, 100, "<=")
	passed.Status = "passed"
	root := testRunReceipt{Status: "passed", Resilience: &testResilienceEvidence{History: []testRunReceipt{a, passed}}}
	text := renderTestDigest([]testRunReceipt{root}, 5, "https://example.test/results.html")
	for _, expected := range []string{"1 passed, 0 failed", "1 / 2 executed runs", "## Cleanup errors (1)", "namespace remains", "#run-evidence-0", "200.000", "100.000", "gregale test replay"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("digest omitted %q", expected)
		}
	}
	if strings.Contains(text, "<script>") || strings.Contains(text, "[run](javascript:unsafe)") || root.Status != "passed" {
		t.Fatal("saved content escaped its presentation or changed the verdict")
	}
	cleanupOnly := testRunReceipt{Status: "failed", CleanupError: "session remains"}
	text = renderTestDigest([]testRunReceipt{cleanupOnly}, 5, "")
	if !strings.Contains(text, "Failure groups (0 total)") || !strings.Contains(text, "session remains") {
		t.Fatal("cleanup-only failure became an assertion group or disappeared")
	}
}

func TestDigestBoundsGroupsAndKeepsOtherFailures(t *testing.T) {
	// adr: 984 — compact views disclose omissions and do not hide non-load failures.
	a := groupingTestReceipt("a", 200, 100, "<=")
	b := groupingTestReceipt("b", 300, 100, "<=")
	b.Load.ChaosPhases[0].Name = "second-phase"
	r := testRunReceipt{Status: "failed", Resilience: &testResilienceEvidence{History: []testRunReceipt{a, b}}}
	text := renderTestDigest([]testRunReceipt{r, {Scenario: "setup", Status: "failed", Error: "deployment failed"}}, 1, "")
	if !strings.Contains(text, "1 additional groups omitted") || !strings.Contains(text, "deployment failed") ||
		!strings.Contains(text, "top-level receipt") {
		t.Fatal("digest omitted the truncation notice or non-assertion failure")
	}
}

func TestDigestCLIRejectsAliasesAndUnsafeURLs(t *testing.T) {
	// adr: 984 — offline reporting must preserve its original evidence file.
	dir := t.TempDir()
	report := filepath.Join(dir, "results.json")
	body, err := json.Marshal([]testRunReceipt{groupingTestReceipt("run", 200, 100, "<=")})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(report, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if code := cmdTestDigest([]string{"--from", report, "--markdown", report}); code == 0 {
		t.Fatal("digest allowed overwriting its source report")
	}
	after, err := os.ReadFile(report)
	if err != nil || !bytes.Equal(body, after) {
		t.Fatal("source report changed")
	}
	for _, value := range []string{"javascript:unsafe", "http://example.test/report", "https://user:secret@example.test/report", "https://example.test/report?token=secret", "https://example.test/report#other"} {
		if _, err := validateTestDigestURL(value); err == nil {
			t.Fatalf("accepted unsafe or ambiguous URL %q", value)
		}
	}
	output := filepath.Join(dir, "digest.md")
	if code := cmdTestDigest([]string{"--from", report, "--markdown", output, "--limit", "1"}); code != 0 {
		t.Fatalf("offline digest returned %d", code)
	}
	result, err := os.ReadFile(output)
	if err != nil || !strings.Contains(string(result), "1 / 1 executed runs") {
		t.Fatal("digest command did not write the report")
	}
}

func TestDigestRejectsInvalidArgumentsAndControlReplay(t *testing.T) {
	// adr: 984 — invalid output options cannot provision or publish test work.
	for _, args := range [][]string{nil, {"--from", "unused"}, {"--from", "unused", "--markdown", "unused", "--limit", "21"}, {"unexpected"}} {
		if code := cmdTestDigest(args); code == 0 {
			t.Fatalf("accepted invalid arguments %v", args)
		}
	}
	r := groupingTestReceipt("run\n# injected", 200, 100, "<=")
	text := renderTestDigest([]testRunReceipt{r}, 5, "")
	if !strings.Contains(text, "Replay omitted") || strings.Contains(text, "\n# injected") {
		t.Fatal("control characters escaped the digest")
	}
}

func TestDigestReplayTextPreservesLiteralCommand(t *testing.T) {
	// adr: 984 — presentation escaping must not change the replay selector.
	const command = "gregale test replay --run-id '[x](https://example.test)*_unsafe_'"
	encoded := testDigestReplayText(command)
	if html.UnescapeString(encoded) != command || strings.Contains(encoded, "[x]") ||
		strings.Contains(encoded, "https://") || strings.Contains(encoded, "*_") {
		t.Fatal("replay escaping changed its text or retained Markdown syntax")
	}
}
