package routeimpact

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func semanticFixture(t *testing.T, before, after map[string]string) Report {
	t.Helper()
	return compareFixture(t, testSnapshot(before), testSnapshot(after), indexFixture(t, before, ""), indexFixture(t, after, ""))
}

func cloneSources(sources map[string]string) map[string]string {
	out := make(map[string]string, len(sources))
	for file, body := range sources {
		out[file] = body
	}
	return out
}

func TestFunctionImpactIgnoresFormattingAndUnrelatedBodies(t *testing.T) {
	before := map[string]string{
		"main.py":    "from fastapi import FastAPI\nfrom helpers import price\napp=FastAPI()\n@app.get('/a')\ndef a(): return price()\n@app.get('/b')\ndef b(): return 1\ndef unused(): return 1\n",
		"helpers.py": "def price(): return 1\ndef unrelated(): return 1\n",
	}
	for _, file := range []string{"main.py", "helpers.py"} {
		t.Run(file+" formatting", func(t *testing.T) {
			after := cloneSources(before)
			after[file] = "# a review comment\n\n" + strings.ReplaceAll(before[file], "return 1", "return ( 1 ) # still one")
			report := semanticFixture(t, before, after)
			if report.Version != 2 || report.Status != "complete" || len(report.ChangedFiles) != 1 || len(report.ChangedSymbols) != 0 || report.Summary.NoLinkedChanges != 2 {
				t.Fatalf("formatting affected routes: %+v", report)
			}
		})
	}
	for _, name := range []string{"unused", "unrelated"} {
		t.Run(name, func(t *testing.T) {
			after := cloneSources(before)
			for file, body := range after {
				after[file] = strings.ReplaceAll(body, "def "+name+"(): return 1", "def "+name+"(): return 2")
			}
			report := semanticFixture(t, before, after)
			if len(report.ChangedSymbols) != 1 || report.Summary.NoLinkedChanges != 2 || report.Status != "complete" {
				t.Fatalf("unrelated body affected routes: %+v", report)
			}
		})
	}
	after := cloneSources(before)
	after["main.py"] = strings.Replace(before["main.py"], "def b(): return 1", "def b(): return 2", 1)
	report := semanticFixture(t, before, after)
	if report.Summary.SourceChanged != 1 || report.Summary.NoLinkedChanges != 1 || report.Routes[1].Change != "source_changed" {
		t.Fatalf("sibling handler body affected route: %+v", report)
	}
}

func TestFunctionImpactResolvesAliasesRelativeReExportsAndLocalImports(t *testing.T) {
	before := map[string]string{
		"main.py":         "from fastapi import FastAPI\nfrom api import compute as calculate\napp=FastAPI()\n@app.get('/a')\ndef a(): return calculate()\n@app.get('/b')\ndef b(): return 1\n",
		"api/__init__.py": "from .pricing import compute\n",
		"api/pricing.py":  "def compute():\n    from .tax import rate as tax_rate\n    return tax_rate()\n",
		"api/tax.py":      "def rate(): return 1\ndef unrelated(): return 1\n",
	}
	after := cloneSources(before)
	after["api/tax.py"] = strings.Replace(before["api/tax.py"], "def rate(): return 1", "def rate(): return 2", 1)
	report := semanticFixture(t, before, after)
	if report.Status != "complete" || report.Summary.PotentiallyAffected != 1 || report.Summary.NoLinkedChanges != 1 || report.Routes[0].Precision != "function" {
		t.Fatalf("function graph: %+v", report)
	}
	if len(report.Routes[0].Evidence) != 2 {
		t.Fatal(report.Routes[0].Evidence)
	}
	for _, evidence := range report.Routes[0].Evidence {
		var names []string
		for _, location := range evidence.ViaSymbols {
			names = append(names, location.Name)
		}
		if evidence.Kind != "function_reference" || evidence.Symbol != "api.tax.rate" || !reflect.DeepEqual(names, []string{"main.a", "api.pricing.compute", "api.tax.rate"}) {
			t.Fatalf("missing symbol chain: %+v", evidence)
		}
	}
	first, _ := json.Marshal(report)
	second, _ := json.Marshal(semanticFixture(t, before, after))
	if string(first) != string(second) {
		t.Fatal("semantic reports are not deterministic")
	}
}

func TestFunctionImpactFastAPIDependencyRoots(t *testing.T) {
	before := map[string]string{
		"main.py": "from fastapi import FastAPI, APIRouter, Depends\nfrom deps import global_auth, router_auth, included_auth, route_auth, auth\napp=FastAPI(dependencies=[Depends(global_auth)])\nrouter=APIRouter(dependencies=[Depends(router_auth)])\n@router.get('/a', dependencies=[Depends(route_auth)])\ndef a(auth=Depends(auth)): return auth\napp.include_router(router, dependencies=[Depends(included_auth)])\n",
		"deps.py": "def global_auth(): return 1\ndef router_auth(): return 1\ndef included_auth(): return 1\ndef route_auth(): return 1\ndef auth(): return 1\ndef unused(): return 1\n",
	}
	for _, name := range []string{"global_auth", "router_auth", "included_auth", "route_auth", "auth", "unused"} {
		t.Run(name, func(t *testing.T) {
			after := cloneSources(before)
			after["deps.py"] = strings.Replace(before["deps.py"], "def "+name+"(): return 1", "def "+name+"(): return 2", 1)
			report := semanticFixture(t, before, after)
			if report.Status != "complete" || report.Summary.SourceChanged != 0 {
				t.Fatalf("dependency precision: %+v", report)
			}
			if name == "unused" {
				if report.Summary.NoLinkedChanges != 1 {
					t.Fatal(report)
				}
			} else if report.Summary.PotentiallyAffected != 1 || report.Routes[0].Evidence[0].Symbol != "deps."+name {
				t.Fatalf("dependency root missing: %+v", report)
			}
		})
	}
}

func TestFunctionImpactModelMetadataKeepsScopedModuleCoverage(t *testing.T) {
	before := map[string]string{
		"main.py":    "from fastapi import FastAPI\nfrom models import Payload\napp=FastAPI()\n@app.post('/a')\ndef a(payload: Payload): return 1\n@app.get('/b')\ndef b(): return 1\n",
		"models.py":  "from pydantic import BaseModel, field_validator\nimport helpers\nclass Payload(BaseModel):\n    value: str\n    @field_validator('value')\n    def validate(cls, value): return helpers.validate(value)\n",
		"helpers.py": "def validate(value): return value\n",
	}
	after := cloneSources(before)
	after["helpers.py"] = "def validate(value): return value+1\n"
	report := semanticFixture(t, before, after)
	if report.Status != "complete" || report.Summary.PotentiallyAffected != 1 || report.Summary.NoLinkedChanges != 1 || report.Routes[0].Precision != "mixed" || report.Routes[0].Evidence[0].Kind != "module_fallback" {
		t.Fatalf("model validation dependency lost or leaked to sibling: %+v", report)
	}
	if !reflect.DeepEqual(report.Routes[0].After.FallbackFiles, []string{"models.py"}) {
		t.Fatal(report.Routes[0].After.FallbackFiles)
	}
}

func TestFunctionImpactUnresolvedCallsKeepModuleFallback(t *testing.T) {
	cases := map[string]string{
		"method":             "def a(): return service.run()\n",
		"parameter":          "def a(helper): return helper()\n",
		"local alias":        "def a():\n    callback=helper\n    return callback()\n",
		"dynamic lookup":     "def a(): return getattr(service, 'run')()\n",
		"nested factory":     "def a():\n    def inner(): return helper()\n    return inner()\n",
		"conditional import": "def a(flag):\n    if flag:\n        from helpers import helper\n    return helper()\n",
		"rebound import":     "helper=lambda: 1\ndef a(): return helper()\n",
		"conditional rebind": "if enabled:\n    helper=lambda: 1\ndef a(): return helper()\n",
		"loop rebind":        "for helper in callbacks:\n    pass\ndef a(): return helper()\n",
		"exception binding":  "def a():\n    try:\n        raise Exception()\n    except Exception as helper:\n        return helper()\n",
		"attribute rebind":   "import helpers\nhelpers.helper=replacement\ndef a(): return helpers.helper()\n",
	}
	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			before := map[string]string{
				"main.py":    "from fastapi import FastAPI\nfrom helpers import helper\napp=FastAPI()\n" + strings.Replace(handler, "def a(", "@app.get('/a')\ndef a(", 1) + "@app.get('/b')\ndef b(): return 1\n",
				"helpers.py": "def helper(): return 1\n",
			}
			after := cloneSources(before)
			after["helpers.py"] = "def helper(): return 2\n"
			report := semanticFixture(t, before, after)
			if report.Status != "incomplete" || report.Routes[0].Precision != "module_fallback" || len(report.Routes[0].Uncertainties) == 0 || report.Routes[0].Change != "potentially_affected" || report.Routes[1].Change != "no_linked_changes" {
				t.Fatalf("uncertainty lost or leaked to sibling: %+v", report)
			}
			found := false
			for _, evidence := range report.Routes[0].Evidence {
				found = found || evidence.Kind == "module_fallback"
			}
			if !found {
				t.Fatal("missing module fallback evidence")
			}
		})
	}
}

func TestFunctionImpactRemovedReferencesAndInitializationChanges(t *testing.T) {
	before := map[string]string{
		"main.py":    "from fastapi import FastAPI\nimport helpers\napp=FastAPI()\n@app.get('/a')\ndef a(): return helpers.helper()\n@app.get('/b')\ndef b(): return 1\n",
		"helpers.py": "value=1\ndef helper(): return value\n",
	}
	after := cloneSources(before)
	after["helpers.py"] = "value=1\n"
	after["main.py"] = strings.Replace(before["main.py"], "return helpers.helper()", "return 1", 1)
	report := semanticFixture(t, before, after)
	found := false
	for _, evidence := range report.Routes[0].Evidence {
		found = found || evidence.Symbol == "helpers.helper" && evidence.Change == "removed" && evidence.Revision == "base"
	}
	if !found || report.Summary.SourceChanged != 1 {
		t.Fatalf("removed reference lost: %+v", report)
	}
	after = cloneSources(before)
	after["helpers.py"] = strings.Replace(before["helpers.py"], "value=1", "value=2", 1)
	report = semanticFixture(t, before, after)
	if report.Summary.PotentiallyAffected != 2 || len(report.ChangedSymbols) != 0 || report.Routes[1].Evidence[0].Kind != "module_initialization" {
		t.Fatalf("import-time changes no longer conservative: %+v", report)
	}
}

func TestFunctionImpactCallbacksAndCycles(t *testing.T) {
	before := map[string]string{
		"main.py": "from fastapi import FastAPI\napp=FastAPI()\n@app.get('/a')\ndef a(): return list(map(helper, []))\ndef helper(): return recursive()\ndef recursive(): return helper()\n",
	}
	after := cloneSources(before)
	after["main.py"] = strings.Replace(before["main.py"], "return recursive()", "return recursive()+1", 1)
	report := semanticFixture(t, before, after)
	if report.Status != "complete" || report.Summary.PotentiallyAffected != 1 || report.Routes[0].Evidence[0].Symbol != "main.helper" {
		t.Fatalf("callback or recursive references: %+v", report)
	}
}

func TestFunctionImpactModuleInitializationCalls(t *testing.T) {
	before := map[string]string{
		"main.py":      "from fastapi import FastAPI\nfrom endpoints import handle\nfrom startup import configure\nconfigure()\napp=FastAPI()\napp.add_api_route('/a', handle)\n",
		"endpoints.py": "def handle(): return 1\n",
		"startup.py":   "from settings import read\ndef configure(): return read()\n",
		"settings.py":  "def read(): return 1\ndef unused(): return 1\n",
	}
	after := cloneSources(before)
	after["settings.py"] = strings.Replace(before["settings.py"], "def read(): return 1", "def read(): return 2", 1)
	report := semanticFixture(t, before, after)
	if report.Status != "complete" || report.Summary.PotentiallyAffected != 1 || report.Routes[0].Precision != "mixed" {
		t.Fatalf("startup function change ignored: %+v", report)
	}
	for _, row := range report.Routes[0].Evidence {
		if row.Kind != "module_initialization" || row.Symbol != "settings.read" || len(row.ViaSymbols) != 3 || row.ViaSymbols[0].File != "main.py" {
			t.Fatalf("startup chain missing: %+v", row)
		}
	}
	after["settings.py"] = strings.Replace(before["settings.py"], "def unused(): return 1", "def unused(): return 2", 1)
	report = semanticFixture(t, before, after)
	if report.Summary.NoLinkedChanges != 1 || report.Status != "complete" {
		t.Fatalf("unused startup helper affected route: %+v", report)
	}
	after = cloneSources(before)
	after["startup.py"] = "import settings\ndef configure(): return settings.service.configure()\n"
	before = cloneSources(after)
	after["settings.py"] = strings.Replace(before["settings.py"], "def unused(): return 1", "def unused(): return 2", 1)
	report = semanticFixture(t, before, after)
	if report.Status != "incomplete" || report.Routes[0].Precision != "module_fallback" || report.Summary.PotentiallyAffected != 1 {
		t.Fatalf("unresolved startup call lost fallback coverage: %+v", report)
	}
}

func TestFunctionImpactMaterializedFunctionMetadata(t *testing.T) {
	before := map[string]string{"main.py": "from fastapi import FastAPI\napp=FastAPI()\ndef helper():\n    'old description'\n    return 1\ndescription=helper.__doc__\n@app.get('/a')\ndef a(): return description\n"}
	after := cloneSources(before)
	after["main.py"] = strings.Replace(before["main.py"], "old description", "new description", 1)
	report := semanticFixture(t, before, after)
	if report.Status != "complete" || report.Summary.PotentiallyAffected != 1 || report.Routes[0].Evidence[0].Kind != "module_initialization" || report.Routes[0].Evidence[0].Symbol != "main.helper" {
		t.Fatalf("materialized metadata change ignored: %+v", report)
	}
}

func TestFunctionImpactAmbiguousImportedEndpointBinding(t *testing.T) {
	sources := map[string]string{
		"main.py":      "from fastapi import FastAPI\nfrom endpoints import handle\nhandle=wrap(handle)\napp=FastAPI()\napp.add_api_route('/a', handle)\n",
		"endpoints.py": "def handle(): return 1\n",
	}
	index := indexFixture(t, sources, "")
	found := false
	for _, issue := range index.Issues {
		found = found || issue.Code == "ambiguous_handler_binding"
	}
	if !found {
		t.Fatalf("rebound endpoint appeared stable: %+v", index)
	}
}

func TestFunctionGraphBoundsFailWithoutTruncation(t *testing.T) {
	index := sourceIndex{Symbols: map[string]functionSymbol{}}
	for i := 0; i <= api.RouteImpactMaxGraphDepth; i++ {
		name := fmt.Sprintf("m.f%d", i)
		index.Symbols[name] = functionSymbol{Name: name, File: "m.py", Line: i + 1, References: []string{fmt.Sprintf("m.f%d", i+1)}}
	}
	if _, _, err := preciseEvidence(Route{HandlerSymbol: "m.f0"}, index, nil, nil, nil, "base"); err == nil {
		t.Fatal("deep function graph silently truncated")
	}
	requirePython(t)
	var source strings.Builder
	for i := 0; i <= api.RouteImpactMaxSymbols; i++ {
		fmt.Fprintf(&source, "def f%d(): pass\n", i)
	}
	if _, err := indexSource(context.Background(), testSnapshot(map[string]string{"m.py": source.String()}), ""); err == nil {
		t.Fatal("excess functions silently truncated")
	}
	source.Reset()
	source.WriteString("def helper(): pass\n")
	previous := "helper"
	for i := 0; i <= api.RouteImpactMaxGraphDepth; i++ {
		name := fmt.Sprintf("alias%d", i)
		fmt.Fprintf(&source, "%s=%s\n", name, previous)
		previous = name
	}
	fmt.Fprintf(&source, "def handle(): return %s()\n", previous)
	if _, err := indexSource(context.Background(), testSnapshot(map[string]string{"m.py": source.String()}), ""); err == nil {
		t.Fatal("excess alias depth silently truncated")
	}
	source.Reset()
	functions := 1
	for functions*functions <= api.RouteImpactMaxSymbolEdges {
		functions++
	}
	var names []string
	for i := 0; i < functions; i++ {
		names = append(names, fmt.Sprintf("f%d", i))
	}
	for _, name := range names {
		fmt.Fprintf(&source, "def %s(): return (%s)\n", name, strings.Join(names, ","))
	}
	if _, err := indexSource(context.Background(), testSnapshot(map[string]string{"m.py": source.String()}), ""); err == nil {
		t.Fatal("excess symbol references silently truncated")
	}
	source.Reset()
	for i := 0; i <= api.RouteImpactMaxSymbolIssues; i++ {
		fmt.Fprintf(&source, "def f%d(): return unresolved()\n", i)
	}
	if _, err := indexSource(context.Background(), testSnapshot(map[string]string{"m.py": source.String()}), ""); err == nil {
		t.Fatal("excess symbol issues silently truncated")
	}
}

func TestFunctionImpactDynamicExecutionInvalidatesRegistrationCompleteness(t *testing.T) {
	for _, statement := range []string{"exec(code)", "from builtins import exec as execute\nexecute(code)"} {
		t.Run(statement, func(t *testing.T) {
			index := indexFixture(t, map[string]string{"main.py": "from fastapi import FastAPI\napp=FastAPI()\n" + statement + "\n@app.get('/a')\ndef a(): return 1\n"}, "")
			found := false
			for _, issue := range index.Issues {
				found = found || issue.Code == "dynamic_execution"
			}
			if !found {
				t.Fatalf("dynamic execution left catalog complete: %+v", index)
			}
		})
	}
}
