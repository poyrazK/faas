package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRecoveryRoutesHaveTypedClientCoverage(t *testing.T) {
	root, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	spec, err := loadSpec(filepath.Join(root, specRelPath))
	if err != nil {
		t.Fatal(err)
	}
	methods, err := loadClientMethods(filepath.Join(root, sdkRelPath))
	if err != nil {
		t.Fatal(err)
	}
	if report := analyze(spec, methods); !report.ok() {
		t.Fatalf("missing client coverage: %v", report.missing)
	}
	delete(methods, "EvaluateWorkflowRecoveryPolicy")
	report := analyze(spec, methods)
	if report.ok() || !strings.Contains(strings.Join(report.missing, "\n"), "/recovery-policy/evaluate") {
		t.Fatalf("missing recovery method was not detected: %v", report.missing)
	}
}
