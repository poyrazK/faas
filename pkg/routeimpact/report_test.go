package routeimpact

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func validReportFixture() Report {
	route := Route{Method: "GET", Path: "/items/{id}", Handler: "item", HandlerSymbol: "main.item", Source: Location{"main.py", 5}, Registration: Location{"main.py", 4}, ContextFiles: []string{"main.py"}, DependencyFiles: []string{}, DependencySymbols: []string{}, FallbackFiles: []string{}}
	return Report{Version: 2, Framework: "fastapi", Repository: "github.com/team/service", SourceRoot: ".", Status: "complete", Scope: scopeDescription,
		Base:         Snapshot{Revision: strings.Repeat("a", 40), SourceSHA256: strings.Repeat("1", 64), PythonFiles: 1, Entrypoint: "main:app"},
		Candidate:    Snapshot{Revision: strings.Repeat("b", 40), SourceSHA256: strings.Repeat("2", 64), PythonFiles: 1, Entrypoint: "main:app"},
		ChangedFiles: []FileChange{{"main.py", "modified"}}, ChangedSymbols: []SymbolChange{}, Issues: []Issue{}, Summary: Summary{SourceChanged: 1},
		Routes: []Result{{Method: route.Method, Path: route.Path, Change: "source_changed", Precision: "function", Before: &route, After: &route, Uncertainties: []Issue{},
			Evidence: []Evidence{{File: "main.py", Change: "modified", Revision: "candidate", Kind: "function_reference", Symbol: "main.item", Line: 5, Via: []string{}, ViaSymbols: []SymbolLocation{{"main.item", "main.py", 5}}}},
		}},
	}
}

func TestParseReportValidAndLegacyProvenance(t *testing.T) {
	for _, mutate := range []func(*Report){
		func(*Report) {},
		func(r *Report) { r.Repository = "" },
		func(r *Report) { r.Candidate.Revision = "working-tree" },
		func(r *Report) {
			r.Status = "incomplete"
			r.Issues = []Issue{{Code: "dynamic_call", Message: "Unresolved reference"}}
		},
	} {
		report := validReportFixture()
		mutate(&report)
		body, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := ParseReport(body)
		if err != nil || parsed.Candidate.Revision != report.Candidate.Revision {
			t.Fatalf("parsed=%+v error=%v", parsed, err)
		}
	}
}

func TestParseReportRejectsMalformedArtifacts(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Report)
	}{
		{"version", func(r *Report) { r.Version = 1 }},
		{"framework", func(r *Report) { r.Framework = "flask" }},
		{"short revision", func(r *Report) { r.Candidate.Revision = "abc" }},
		{"baseline worktree", func(r *Report) { r.Base.Revision = "working-tree" }},
		{"fingerprint", func(r *Report) { r.Base.SourceSHA256 = "secret-value" }},
		{"repository URL", func(r *Report) { r.Repository = "https://secret-value@github.com/team/service" }},
		{"root", func(r *Report) { r.SourceRoot = "../service" }},
		{"collections", func(r *Report) { r.Routes = nil }},
		{"route bounds", func(r *Report) { r.Routes = make([]Result, api.RouteImpactMaxComparedRoutes+1) }},
		{"summary", func(r *Report) { r.Summary.SourceChanged = 2 }},
		{"duplicate route", func(r *Report) { r.Routes = append(r.Routes, r.Routes[0]); r.Summary.SourceChanged++ }},
		{"method", func(r *Report) { r.Routes[0].Method = "get" }},
		{"controls", func(r *Report) { r.Routes[0].Path = "/secret-value\n" }},
		{"added has baseline", func(r *Report) { r.Routes[0].Change = "added"; r.Summary = Summary{Added: 1} }},
		{"invalid location", func(r *Report) { r.Routes[0].After.Source.File = "../secret-value" }},
		{"empty function chain", func(r *Report) { r.Routes[0].Evidence[0].ViaSymbols = nil }},
		{"bad evidence revision", func(r *Report) { r.Routes[0].Evidence[0].Revision = "other" }},
		{"bad evidence kind", func(r *Report) { r.Routes[0].Evidence[0].Kind = "secret-value" }},
		{"false completeness", func(r *Report) { r.Routes[0].Uncertainties = []Issue{{Code: "dynamic_call", Message: "secret-value"}} }},
		{"symbol name conflict", func(r *Report) {
			r.ChangedSymbols = []SymbolChange{{Name: "main.item", Change: "added", After: &SymbolLocation{"main.other", "main.py", 5}}}
		}},
		{"depth bound", func(r *Report) {
			r.Routes[0].Evidence[0].ViaSymbols = make([]SymbolLocation, api.RouteImpactMaxGraphDepth+1)
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			report := validReportFixture()
			test.mutate(&report)
			body, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ParseReport(body); err == nil || strings.Contains(err.Error(), "secret-value") {
				t.Fatalf("unsafe acceptance/error: %v", err)
			}
		})
	}
}

func TestParseReportStrictJSON(t *testing.T) {
	body, err := json.Marshal(validReportFixture())
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		string(body) + " {}", strings.Replace(string(body), `"version":2`, `"version":2,"Version":2`, 1), strings.Replace(string(body), `"version":2`, `"version":2,"version":2`, 1),
		strings.Replace(string(body), `"python_files":1`, `"python_files":1,"python_files":1`, 1),
		strings.Replace(string(body), `"version":2`, `"secret-value":"extra","version":2`, 1),
		`{"version":` + strings.Repeat("[", api.RouteImpactReportJSONMaxDepth+1) + strings.Repeat("]", api.RouteImpactReportJSONMaxDepth+1) + `}`,
		`null`, `[]`, `{"secret-value":`,
	} {
		if _, err := ParseReport([]byte(invalid)); err == nil || strings.Contains(err.Error(), "secret-value") {
			t.Fatalf("unsafe acceptance/error: %v", err)
		}
	}
	if _, err := ParseReport(make([]byte, api.RouteImpactReportMaxBytes+1)); err == nil {
		t.Fatal("accepted oversized file")
	}
}

func TestRepositoryReferenceCanonicalAndCredentialFree(t *testing.T) {
	commit := strings.Repeat("a", 40)
	for _, raw := range []string{"github.com/Team/Service", "git@github.com:Team/Service.git", "https://user:secret-value@github.com/Team/Service.git", "ssh://git@github.com/Team/Service.git", "github://Team/Service@" + commit} {
		identity, revision := RepositoryReference(raw)
		if identity != "github.com/team/service" || (strings.HasPrefix(raw, "github://") && revision != commit) {
			t.Fatalf("%q => %q @ %q", raw, identity, revision)
		}
	}
	for _, raw := range []string{"https://github.com.evil/team/service", "https://github.com/team/service?secret-value=1", "https://github.com/team/service#x", "github://team/service@abc", "https://github.com/team/service/extra", "https://github.com/team%2fservice", " github.com/team/service", "https://github.com:443/team/service", "github.com/../service", "file:///secret-value", "https://gitlab.com/team/service"} {
		if identity, _ := RepositoryReference(raw); identity != "" {
			t.Fatalf("accepted %q as %q", raw, identity)
		}
	}
}

func TestRepositoryReferenceCanonicalIdentityIsIdempotent(t *testing.T) {
	for _, raw := range []string{"git@github.com:Team/Service.git.git", "github.com/team/service.git", "github://team/service.git@" + strings.Repeat("a", 40)} {
		identity, _ := RepositoryReference(raw)
		if identity != "github.com/team/service.git" {
			t.Fatalf("identity=%q", identity)
		}
		if again, _ := RepositoryReference(identity); again != identity {
			t.Fatalf("identity changed on revalidation: %q => %q", identity, again)
		}
		report := validReportFixture()
		report.Repository = identity
		body, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ParseReport(body); err != nil {
			t.Fatal(err)
		}
	}
}
