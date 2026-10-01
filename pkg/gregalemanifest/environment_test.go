package gregalemanifest_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

func TestParseEnvironmentRejectsAmbiguousDefinitions(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"empty", ""},
		{"multiple documents", "project: shop\n---\nproject: other"},
		{"duplicate YAML key", "project: shop\nproject: other"},
		{"duplicate JSON key", `{"project":"shop","project":"other"}`},
		{"nested duplicate", "workloads:\n  api:\n    variables:\n      MODE: a\n      MODE: b"},
		{"unknown root", "reconcile_everything: true"},
		{"unknown workload", "workloads:\n  api:\n    varibles: {}"},
		{"unknown binding", "workloads:\n  api:\n    queue_bindings:\n      jobs:\n        qeuue_name: jobs"},
		{"alias", "configuration:\n  one: &one value\n  two: *one"},
		{"merge", "configuration:\n  <<: {mode: production}"},
		{"timestamp", "configuration:\n  date: 2026-09-30"},
		{"non-string key", "configuration:\n  1: value"},
		{"sequence", "- one\n- two"},
		{"nonfinite", "configuration:\n  value: .inf"},
		{"too large", strings.Repeat(" ", api.EnvironmentGitOpsMaxDefinitionBytes+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := gregalemanifest.ParseEnvironment([]byte(tc.raw)); err == nil {
				t.Fatal("ambiguous or invalid definition accepted")
			}
		})
	}
}

func TestParseEnvironmentYAMLAndJSONHaveOneContract(t *testing.T) {
	yaml := `api_version: gregale.dev/environment/v1
project: shop
environment: production
configuration:
  order_id: 9007199254740993
workloads:
  api:
    app: shop-api
    source:
      directory: ./api
    variables:
      MODE: production
    routes:
      only_allow_declared_routes: true
      declared_routes:
        - path: /orders
          methods: [post, GET]
    policies: []
`
	json := `{"api_version":"gregale.dev/environment/v1","project":"shop","environment":"production","configuration":{"order_id":9007199254740993},"workloads":{"api":{"app":"shop-api","source":{"directory":"api"},"variables":{"MODE":"production"},"routes":{"only_allow_declared_routes":true,"declared_routes":[{"path":"/orders","methods":["GET","POST"]}]},"policies":[]}}}`
	var digests []string
	for _, raw := range []string{yaml, json} {
		definition, err := gregalemanifest.ParseEnvironment([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		compiled, err := environmentsync.Compile(definition)
		if err != nil {
			t.Fatal(err)
		}
		if string(compiled.Definition.Configuration["order_id"]) != "9007199254740993" {
			t.Fatal("integer precision lost")
		}
		if compiled.Definition.Workloads["api"].Policies == nil {
			t.Fatal("explicit empty policy collection became unmanaged")
		}
		digests = append(digests, compiled.Digest)
	}
	if digests[0] != digests[1] {
		t.Fatalf("YAML and JSON normalize differently: %v", digests)
	}
}

func TestParseAndCompileEnvironmentExplicitEmptyMembership(t *testing.T) {
	prefix := "api_version: gregale.dev/environment/v1\nproject: shop\nenvironment: production\n"
	for _, raw := range []string{prefix, prefix + "workloads: null\n"} {
		definition, err := gregalemanifest.ParseEnvironment([]byte(raw))
		if err == nil {
			_, err = environmentsync.Compile(definition)
		}
		if err == nil {
			t.Fatal("missing or null workload membership was accepted")
		}
	}
	for _, raw := range []string{prefix + "workloads: {}\n", `{"api_version":"gregale.dev/environment/v1","project":"shop","environment":"production","workloads":{}}`} {
		definition, err := gregalemanifest.ParseEnvironment([]byte(raw))
		if err != nil || definition.Workloads == nil {
			t.Fatalf("explicit empty membership: %+v %v", definition, err)
		}
		compiled, err := environmentsync.Compile(definition)
		if err != nil || len(compiled.Fields) != 0 {
			t.Fatalf("compile empty membership: %+v %v", compiled, err)
		}
	}
}
