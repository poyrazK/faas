package main

import (
	"encoding/json"
	"flag"
	"io"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestValidateParamFlagsBuildParameterOnlyAction(t *testing.T) {
	fs := flag.NewFlagSet("edge-rules create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	params := addValidateParamFlags(fs)
	if err := fs.Parse([]string{
		"--validate-path-template", "/users/{id}",
		"--validate-path-schema", `{"type":"object","properties":{"id":{"type":"integer"}},"required":["id"]}`,
		"--validate-query-schema", `{"type":"object","properties":{"limit":{"type":"integer"}}}`,
	}); err != nil {
		t.Fatal(err)
	}
	visited := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { visited[f.Name] = true })
	if !edgeRuleValidateFlagsVisited(visited) {
		t.Fatal("parameter flags are not counted as --validate-* flags")
	}
	resolved, err := params.resolve(visited)
	if err != nil || resolved == nil || resolved.PathTemplate != "/users/{id}" || len(resolved.Headers) != 0 {
		t.Fatalf("resolve = %+v, %v", resolved, err)
	}
	raw, err := buildEdgeRuleAction("validate", edgeRuleActionInputs{ValidateParameters: resolved})
	if err != nil {
		t.Fatalf("parameter-only action rejected: %v", err)
	}
	var action api.EdgeRuleValidateAction
	if err := json.Unmarshal(raw, &action); err != nil || action.Parameters == nil || len(action.Schema) != 0 {
		t.Fatalf("action = %s (%v)", raw, err)
	}

	// No flags: no parameters, so a body schema is still required.
	empty := addValidateParamFlags(flag.NewFlagSet("x", flag.ContinueOnError))
	if resolved, err := empty.resolve(map[string]bool{}); resolved != nil || err != nil {
		t.Fatalf("empty resolve = %+v, %v", resolved, err)
	}
	if _, err := buildEdgeRuleAction("validate", edgeRuleActionInputs{}); err == nil || !strings.Contains(err.Error(), "schema or parameters") {
		t.Fatalf("empty validate action = %v", err)
	}
}
