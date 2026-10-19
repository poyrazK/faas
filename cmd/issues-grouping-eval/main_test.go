// adr: 1661
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/issues/evaluation"
)

func corpusPath() string { return "../../pkg/issues/evaluation/testdata/corpus.json" }

func invoke(args ...string) (int, string, string) {
	var out, diagnostic bytes.Buffer
	code := run(args, &out, &diagnostic)
	return code, out.String(), diagnostic.String()
}

func TestAggregateModePreservedAndPreviewIsExplicit(t *testing.T) {
	code, out, diagnostic := invoke("-corpus", corpusPath())
	var aggregate []evaluation.Report
	if err := json.Unmarshal([]byte(out), &aggregate); err != nil || code != 0 || diagnostic != "" || len(aggregate) != 3 {
		t.Fatalf("aggregate mode changed: %d %s %v", code, diagnostic, err)
	}
	if strings.Contains(out, "node-checkout") || strings.Contains(out, "decision") {
		t.Fatal("aggregate mode exposed case diagnostics")
	}
	code, out, diagnostic = invoke("-corpus", corpusPath(), "-strategy", "cause-chain-v1", "-compare-with", "failure-site-v1")
	var preview evaluation.Comparison
	if err := json.Unmarshal([]byte(out), &preview); err != nil || code != 0 || diagnostic != "" || len(preview.Changes) == 0 || preview.Regressed || preview.FixedFalseSplitPairs == 0 {
		t.Fatalf("preview missing wrapper fixes: %d %s %+v %v", code, diagnostic, preview, err)
	}
	foundCause := false
	for _, change := range preview.Changes {
		foundCause = foundCause || change.CandidateDecision.CauseIndex != nil
	}
	if !foundCause {
		t.Fatal("cause selection missing from explanation")
	}
}

func TestSavedBaselineCheckAndSafeCreation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "review.json")
	args := []string{"-corpus", corpusPath(), "-strategy", "cause-chain-v1", "-write-baseline", path}
	if code, _, diagnostic := invoke(args...); code != 0 {
		t.Fatalf("create baseline: %s", diagnostic)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("baseline was not private: %v %v", info, err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if code, _, _ := invoke(args...); code != 1 {
		t.Fatal("existing baseline overwritten")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("failed creation changed baseline")
	}
	args = []string{"-corpus", corpusPath(), "-strategy", "cause-chain-v1", "-baseline", path, "-check"}
	code, out, diagnostic := invoke(args...)
	var comparison evaluation.Comparison
	if err := json.Unmarshal([]byte(out), &comparison); err != nil || code != 0 || diagnostic != "" || comparison.Regressed || len(comparison.Changes) != 0 {
		t.Fatalf("same strategy did not pass: %d %s %v", code, diagnostic, err)
	}
	args[3] = "legacy"
	code, out, diagnostic = invoke(args...)
	if err := json.Unmarshal([]byte(out), &comparison); err != nil || code != 2 || !comparison.Regressed || !strings.Contains(diagnostic, "grouping regression") {
		t.Fatalf("regression check lost report or exit status: %d %s %v", code, diagnostic, err)
	}
	args = args[:len(args)-1] // A preview reports regressions but is not a CI verdict.
	if code, _, _ := invoke(args...); code != 0 {
		t.Fatal("preview unexpectedly acted as a check")
	}
	args[3] = "cause-chain-v1"
	err = execute(args, brokenWriter{}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "write grouping report") {
		t.Fatalf("report write failure swallowed: %v", err)
	}
	link := filepath.Join(t.TempDir(), "link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	args[5] = link
	if code, _, _ := invoke(args...); code != 1 {
		t.Fatal("symlink baseline accepted")
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("output unavailable") }

func TestInvalidInputsAndModeCombinations(t *testing.T) {
	for _, args := range [][]string{
		{"-check"},
		{"-baseline", "ignored"},
		{"-strategy", "future"},
		{"-strategy", "legacy", "-baseline", "ignored", "-compare-with", "legacy"},
		{"-strategy", "legacy", "-compare-with", "legacy", "-check"},
		{"-strategy", "legacy", "-write-baseline", "ignored", "-baseline", "ignored"},
		{"unexpected"},
	} {
		args = append([]string{"-corpus", corpusPath()}, args...)
		if code, _, _ := invoke(args...); code != 1 {
			t.Fatalf("invalid options accepted: %v", args)
		}
	}
	if code, _, _ := invoke("-help"); code != 0 {
		t.Fatal("help did not succeed")
	}
	for _, body := range []string{`[] []`, `[{"id":"x","id":"y"}]`, `null`} {
		path := filepath.Join(t.TempDir(), "corpus.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if code, _, _ := invoke("-corpus", path); code != 1 {
			t.Fatal("malformed corpus accepted")
		}
	}
}
