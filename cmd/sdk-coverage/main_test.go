package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRuntimeConnectionReportsRemainWorkloadOnly(t *testing.T) {
	root, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, specRelPath))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Paths map[string]struct {
			Post struct {
				Internal bool                  `yaml:"x-internal"`
				Security []map[string][]string `yaml:"security"`
			}
		}
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	const runtimePath = "/v1/runtime/data-api/connection-observations"
	operation := document.Paths[runtimePath].Post
	if !operation.Internal || len(operation.Security) != 1 || len(operation.Security[0]) != 1 {
		t.Fatalf("SDK exception requires the private workload contract: %+v", operation)
	}
	if _, ok := operation.Security[0]["dataAPIConnectionsWorkloadIdentity"]; !ok {
		t.Fatal("SDK exception requires the dedicated workload audience")
	}
	const budgetPath = "/v1/apps/{slug}/data-api/connection-budget"
	spec := map[string]map[string]any{runtimePath: {"post": true}, budgetPath: {"get": true}}
	if report := analyze(spec, map[string]bool{"GetAppDataAPIConnectionBudgets": true}); !report.ok() {
		t.Fatalf("workload-only report should not require an account SDK method: %v", report.missing)
	}
	if report := analyze(spec, map[string]bool{}); len(report.missing) != 1 || !strings.Contains(report.missing[0], budgetPath) {
		t.Fatalf("public budget SDK coverage must remain required: %v", report.missing)
	}
}

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
