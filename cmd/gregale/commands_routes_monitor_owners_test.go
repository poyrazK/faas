package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routeimpact"
)

func TestRouteMonitorCodeownersLastMatchingRuleWins(t *testing.T) {
	rules := parseRouteMonitorCodeowners([]byte("# top-level fallback\n* @org/default\n/services/api/** @org/api\n/services/api/checkout.go @org/checkout @org/platform\n/services/api/unowned.go\n[invalid] @ignored\n!negated @ignored\n"))
	if len(rules) != 4 {
		t.Fatalf("parsed %d valid CODEOWNERS rules, want 4: %+v", len(rules), rules)
	}
	tests := []struct {
		name       string
		path       string
		wantOwners []string
		wantLine   int
	}{
		{name: "fallback", path: "README.md", wantOwners: []string{"@org/default"}, wantLine: 2},
		{name: "monorepo nested rule", path: "services/api/handlers/checkout.go", wantOwners: []string{"@org/api"}, wantLine: 3},
		{name: "most specific last rule", path: "services/api/checkout.go", wantOwners: []string{"@org/checkout", "@org/platform"}, wantLine: 4},
		{name: "ownerless override", path: "services/api/unowned.go", wantOwners: []string{}, wantLine: 5},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := routeMonitorMatchCodeowners(rules, test.path)
			if got == nil || got.line != test.wantLine || strings.Join(got.owners, ",") != strings.Join(test.wantOwners, ",") {
				t.Fatalf("match = %#v, want owners %v from line %d", got, test.wantOwners, test.wantLine)
			}
		})
	}
}

func TestRouteMonitorCodeownersPathPatterns(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		{pattern: "*.go", path: "services/api/handler.go", want: true},
		{pattern: "docs/*", path: "docs/guide.md", want: true},
		{pattern: "docs/*", path: "docs/nested/guide.md", want: false},
		{pattern: "/docs/*", path: "nested/docs/guide.md", want: false},
		{pattern: "/docs/", path: "nested/docs/guide.md", want: false},
		{pattern: "apps/", path: "nested/apps/service/main.go", want: true},
		{pattern: "**/logs", path: "build/logs/app.txt", want: true},
		{pattern: "*.GO", path: "main.go", want: false},
		{pattern: `literal\*.go`, path: "literal*.go", want: true},
		{pattern: "[ab].go", path: "a.go", want: false},
	}
	for _, test := range tests {
		t.Run(test.pattern+"_"+test.path, func(t *testing.T) {
			if got := routeMonitorCodeownersPatternMatches(test.pattern, test.path); got != test.want {
				t.Fatalf("match(%q, %q) = %t, want %t", test.pattern, test.path, got, test.want)
			}
		})
	}
}

func TestRouteMonitorSourceOwnershipUsesCandidateCodeownersAndSourceRoot(t *testing.T) {
	root := t.TempDir()
	routeImpactGit(t, root, "init", "-q")
	routeImpactGit(t, root, "remote", "add", "origin", "https://github.com/team/service.git")
	codeownersPath := filepath.Join(root, ".github", "CODEOWNERS")
	if err := os.MkdirAll(filepath.Dir(codeownersPath), 0o700); err != nil {
		t.Fatal(err)
	}
	codeowners := "* @org/default\n/services/api/handlers.py @org/checkout\n/services/api/shared/** @org/platform\n/services/api/old.py\n"
	if err := os.WriteFile(codeownersPath, []byte(codeowners), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "CODEOWNERS"), []byte("* @org/lower-priority\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"services/api/handlers.py", "services/api/shared/validation.py", "services/api/old.py"} {
		file := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("# fixture\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	routeImpactGit(t, root, "add", ".")
	routeImpactGit(t, root, "commit", "-qm", "incident candidate")
	candidate := routeMonitorGitOutput(t, root, "rev-parse", "HEAD")
	// A dirty newer rule must not override the CODEOWNERS bytes at the candidate.
	if err := os.WriteFile(codeownersPath, []byte("* @org/wrong\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	correlation := routeMonitorSourceCorrelation{
		Status: "release_pair_bound", Repository: "github.com/team/service", SourceRoot: "services/api", CandidateRevision: candidate,
		Routes: []routeMonitorIncidentSourceRoute{{Source: &routeInvestigationSourceRoute{
			Evidence: []routeimpact.Evidence{{
				File: "handlers.py", Change: "source_changed", Revision: "candidate", Line: 8,
				ViaSymbols: []routeimpact.SymbolLocation{{File: "shared/validation.py", Name: "validate", Line: 3}},
			}},
			BeforeLocation: &routeimpact.Location{File: "old.py", Line: 2},
		}}},
	}
	addRouteMonitorSourceOwners(context.Background(), root, &correlation)
	ownership := correlation.Routes[0].Ownership
	if ownership == nil || ownership.Status != "partially_owned" || ownership.CodeownersFile != ".github/CODEOWNERS" || ownership.CandidateRevision != candidate {
		t.Fatalf("ownership provenance = %+v", ownership)
	}
	if len(ownership.Files) != 3 {
		t.Fatalf("owned source file count = %d, want 3: %+v", len(ownership.Files), ownership.Files)
	}
	want := map[string]struct {
		status string
		owner  string
	}{
		"services/api/handlers.py":          {status: "owned", owner: "@org/checkout"},
		"services/api/shared/validation.py": {status: "owned", owner: "@org/platform"},
		"services/api/old.py":               {status: "unowned"},
	}
	for _, file := range ownership.Files {
		expected, ok := want[file.Path]
		if !ok || file.Status != expected.status || expected.owner != "" && (len(file.Owners) != 1 || file.Owners[0] != expected.owner) {
			t.Errorf("ownership for %s = %+v, want %+v", file.Path, file, expected)
		}
	}

	correlation.Repository = "github.com/other/service"
	addRouteMonitorSourceOwners(context.Background(), root, &correlation)
	if got := correlation.Routes[0].Ownership; got == nil || got.Status != "unavailable" || got.Reason != "local_repository_mismatch" {
		t.Fatalf("mismatched repository ownership = %+v", got)
	}
}

func TestRouteMonitorIncidentSourceCustomerImpactExportsCountsOnly(t *testing.T) {
	incident := cliProductionIncident(t)
	incident.OpeningReport.Customers = &api.RouteMonitorCustomerReport{
		GroupBy: "organization", Coverage: "complete", Routes: []api.RouteMonitorCustomerRoute{{
			Method: "POST", Path: "/checkout", ObservedCustomers: 12, ViolatedCustomers: 4,
			UnknownCustomers: 2, ViolatingCustomerIDs: []string{"customer-secret-id"},
		}},
	}
	routes := correlateRouteMonitorAffectedRoutes(incident, investigationSourceFixture("POST", "/checkout"))
	if len(routes) != 1 || routes[0].CustomerImpactStatus != "available" || routes[0].CustomerImpact == nil {
		t.Fatalf("customer impact was not mapped to the route: %+v", routes)
	}
	impact := routes[0].CustomerImpact
	if impact.ObservedCustomers != 12 || impact.ViolatedCustomers != 4 || impact.UnknownCustomers != 2 || impact.GroupBy != "organization" {
		t.Fatalf("aggregate customer counts = %+v", impact)
	}
	encoded, err := json.Marshal(routes)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "customer-secret-id") {
		t.Fatal("source handoff included customer identities")
	}
}
