package routecontract

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/routeimpact"
)

func contractSource(status string, routes ...routeimpact.Route) routeimpact.Report {
	results := make([]routeimpact.Result, 0, len(routes))
	for _, route := range routes {
		copy := route
		results = append(results, routeimpact.Result{Method: route.Method, Path: route.Path, Change: "no_linked_changes", After: &copy})
	}
	issues := []routeimpact.Issue{}
	if status == "incomplete" {
		issues = append(issues, routeimpact.Issue{Code: "dynamic_route", File: "main.py", Line: 4, Message: "A dynamic route registration could not be resolved."})
	}
	return routeimpact.Report{Version: 2, Framework: "fastapi", App: "demo", SourceRoot: ".", Status: status,
		Base: routeimpact.Snapshot{Revision: "base"}, Candidate: routeimpact.Snapshot{Revision: "candidate"},
		Routes: results, Issues: issues}
}

func contractDocument(paths string) []byte {
	return []byte("openapi: 3.1.0\ninfo:\n  title: Demo\n  version: '1'\npaths:\n" + paths)
}

func TestCompareExactAndUniqueParameterShapeWithConfirmedDrift(t *testing.T) {
	document := contractDocument("  /health:\n    get:\n      responses: {}\n  /users/{userId}:\n    get:\n      responses: {}\n  /contract-only:\n    post:\n      responses: {}\n")
	source := contractSource("complete",
		routeimpact.Route{Method: "GET", Path: "/health", Handler: "main.health", Source: routeimpact.Location{File: "main.py", Line: 2}, Registration: routeimpact.Location{File: "main.py", Line: 1}},
		routeimpact.Route{Method: "GET", Path: "/users/{id}", Handler: "main.get_user", Source: routeimpact.Location{File: "main.py", Line: 5}, Registration: routeimpact.Location{File: "main.py", Line: 4}},
		routeimpact.Route{Method: "DELETE", Path: "/source-only", Handler: "main.delete", Source: routeimpact.Location{File: "main.py", Line: 8}, Registration: routeimpact.Location{File: "main.py", Line: 7}},
	)
	report, err := Compare(document, "openapi.yaml", source)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "complete" || report.Outcome != "drift" || report.Summary != (Summary{ContractRoutes: 3, SourceRoutes: 3, Matched: 2, SourceOnly: 1, ContractOnly: 1}) {
		t.Fatalf("unexpected comparison: %+v", report)
	}
	if len(report.Routes) != 4 {
		t.Fatalf("findings=%d: %+v", len(report.Routes), report.Routes)
	}
	foundShape, foundSourceOnly, foundContractOnly := false, false, false
	for _, finding := range report.Routes {
		switch {
		case finding.Match == "parameter_shape":
			foundShape = finding.ContractPath == "/users/{userId}" && finding.SourcePath == "/users/{id}" && finding.Source != nil && finding.Registration != nil
		case finding.Status == "source_only":
			foundSourceOnly = finding.SourcePath == "/source-only" && finding.Registration != nil
		case finding.Status == "contract_only":
			foundContractOnly = finding.ContractPath == "/contract-only"
		}
	}
	if !foundShape || !foundSourceOnly || !foundContractOnly {
		t.Fatalf("missing useful findings: %+v", report.Routes)
	}
	if len(report.OpenAPISHA256) != 64 || report.OpenAPIVersion != "3.1.0" {
		t.Fatalf("missing document identity: %+v", report)
	}
}

func TestCompareAmbiguousParameterShapeIsInconclusive(t *testing.T) {
	document := contractDocument("  /users/{id}:\n    get:\n      responses: {}\n  /users/{name}:\n    get:\n      responses: {}\n")
	source := contractSource("complete",
		routeimpact.Route{Method: "GET", Path: "/users/{slug}", Handler: "main.by_slug"},
		routeimpact.Route{Method: "GET", Path: "/users/{userId}", Handler: "main.by_id"},
	)
	report, err := Compare(document, "openapi.yaml", source)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "incomplete" || report.Outcome != "incomplete" || report.Summary.Unknown != 2 || report.Summary.SourceOnly != 0 || report.Summary.ContractOnly != 0 {
		t.Fatalf("ambiguous routes were treated as drift: %+v", report)
	}
	for _, finding := range report.Routes {
		if finding.Status != "unknown" || len(finding.Candidates) != 2 {
			t.Fatalf("ambiguous finding lacks candidate evidence: %+v", finding)
		}
	}
}

func TestCompareBoundsAmbiguousCandidateEvidence(t *testing.T) {
	document := contractDocument("  /items/{id}:\n    get:\n      responses: {}\n")
	routes := make([]routeimpact.Route, 0, maxCandidatesPerFinding+5)
	for i := 0; i < maxCandidatesPerFinding+5; i++ {
		routes = append(routes, routeimpact.Route{Method: "GET", Path: "/items/{candidate" + string(rune('a'+i)) + "}"})
	}
	report, err := Compare(document, "openapi.yaml", contractSource("complete", routes...))
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.Unknown != 1 || len(report.Routes) != 1 || len(report.Routes[0].Candidates) != maxCandidatesPerFinding || report.Routes[0].CandidatesOmitted != 5 {
		t.Fatalf("ambiguous evidence was not bounded: summary=%+v finding=%+v", report.Summary, report.Routes)
	}
}

func TestCompareIncompleteSourceDoesNotInventDrift(t *testing.T) {
	document := contractDocument("  /health:\n    get:\n      responses: {}\n  /new:\n    post:\n      responses: {}\n")
	source := contractSource("incomplete",
		routeimpact.Route{Method: "GET", Path: "/health", Handler: "main.health"},
		routeimpact.Route{Method: "GET", Path: "/maybe", Handler: "main.maybe"},
	)
	report, err := Compare(document, "openapi.yaml", source)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "incomplete" || report.Outcome != "incomplete" || report.Summary.Matched != 1 || report.Summary.Unknown != 2 || report.Summary.SourceOnly != 0 || report.Summary.ContractOnly != 0 {
		t.Fatalf("incomplete source generated confirmed drift: %+v", report)
	}
}

func TestCompareTRACEAndUnresolvedPathItemReference(t *testing.T) {
	document := contractDocument("  /trace:\n    trace:\n      responses: {}\n  /external:\n    $ref: ./paths.yaml#/external\n")
	source := contractSource("complete", routeimpact.Route{Method: "TRACE", Path: "/trace", Handler: "main.trace"})
	report, err := Compare(document, "openapi.yaml", source)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "incomplete" || report.Outcome != "incomplete" || report.Summary.Matched != 1 || len(report.ContractIssues) != 1 || report.ContractIssues[0].Code != "unresolved_path_item_reference" {
		t.Fatalf("TRACE or unresolved reference handling is incorrect: %+v", report)
	}
}

func TestCompareRejectsUnsupportedOpenAPIVersionsAndMalformedPaths(t *testing.T) {
	for name, doc := range map[string][]byte{
		"unsupported version": contractDocument("  /health:\n    get:\n      responses: {}\n"),
		"missing paths":       []byte("openapi: 3.1.0\ninfo: {title: Demo, version: '1'}\n"),
	} {
		if name == "unsupported version" {
			doc = []byte(strings.Replace(string(doc), "3.1.0", "3.2.0", 1))
		}
		if _, err := Compare(doc, "openapi.yaml", contractSource("complete")); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	invalidPath := contractDocument("  '/health/{':\n    get:\n      responses: {}\n")
	report, err := Compare(invalidPath, "openapi.yaml", contractSource("complete"))
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "incomplete" || report.Outcome != "incomplete" || len(report.ContractIssues) != 1 || report.ContractIssues[0].Code != "invalid_path_template" {
		t.Fatalf("invalid path template generated a drift result: %+v", report)
	}
}
