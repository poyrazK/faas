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

func TestDevSessionAliasRetainsWireCoverage(t *testing.T) {
	spec := map[string]map[string]any{"/v1/dev/sessions/{project}": {"put": true}}
	methods := map[string]bool{"PutDevSessionsProject": true, "UpsertDevSession": true}
	if report := analyze(spec, methods); len(report.missing) != 0 || len(report.warn) != 0 {
		t.Fatalf("documented alias and wire method: %+v", report)
	}
	delete(methods, "PutDevSessionsProject")
	if report := analyze(spec, methods); len(report.missing) != 1 {
		t.Fatalf("alias hid missing wire method: %+v", report)
	}
	if report := analyze(map[string]map[string]any{}, methods); len(report.warn) != 1 {
		t.Fatalf("alias hid removed spec route: %+v", report)
	}
}

// adr: 1543, 1526, 1527 — named GitOps clients remain mandatory for all three routes.
func TestGitOpsRecoveryRoutesRequireTheirTypedClients(t *testing.T) {
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
	for _, tc := range []struct{ method, route string }{
		{"PreviewEnvironmentGitPromotion", "POST /v1/projects/{slug}/environments/{environment}/gitops/promotion-plan"},
		{"PreviewEnvironmentGitRollback", "POST /v1/projects/{slug}/environments/{environment}/gitops/rollback-plan"},
		{"ListEnvironmentGitReleases", "GET /v1/projects/{slug}/environments/{environment}/gitops/releases"},
	} {
		t.Run(tc.method, func(t *testing.T) {
			if !methods[tc.method] {
				t.Fatal("typed Client method is missing")
			}
			if report := analyze(spec, methods); !report.ok() {
				t.Fatalf("typed clients were not recognized: %v", report.missing)
			}
			delete(methods, tc.method)
			report := analyze(spec, methods)
			methods[tc.method] = true
			if len(report.missing) != 1 || !strings.Contains(report.missing[0], tc.route+" →") {
				t.Fatalf("missing client implementation escaped the coverage gate: %v", report.missing)
			}
		})
	}
}
