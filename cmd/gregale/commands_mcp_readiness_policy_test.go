package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPReadinessPolicyEnvironmentSelection(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/apps/test-mcp/mcp-readiness-policy" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer PLATFORM_ONLY" {
			t.Error("missing platform credential")
		}
		if r.Method == http.MethodGet {
			if r.URL.Query().Get("scope") != "staging" {
				t.Error("GET lost environment")
			}
		} else {
			var request api.SetMCPReadinessPolicyRequest
			if json.NewDecoder(r.Body).Decode(&request) != nil || request.Scope != "staging" || request.ExpectedRevision != "" || request.Reason != "reviewed staging opt out" {
				t.Error("PUT lost policy selection or CAS")
			}
		}
		writeJSONTest(w, api.MCPReadinessPolicy{Scope: "staging"})
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "PLATFORM_ONLY")
	var output bytes.Buffer
	oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
	osStdout, osStderr, jsonOutput = &output, &output, true
	defer func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON }()
	file := filepath.Join(t.TempDir(), "policy.json")
	if e := os.WriteFile(file, []byte(`{"enabled":false,"endpoint":"/mcp","max_age_seconds":300,"reason":"reviewed staging opt out"}`), 0600); e != nil {
		t.Fatal(e)
	}
	for _, args := range [][]string{
		{"readiness", "policy", "get", "--app", "test-mcp", "--scope", "staging"},
		{"readiness", "policy", "set", "--app", "test-mcp", "--scope", "staging", "--file", file},
	} {
		if code := cmdMCP(args); code != 0 {
			t.Fatalf("exit=%d output=%s", code, output.String())
		}
	}
	if calls != 2 {
		t.Fatal("missing policy calls", calls)
	}
}

func TestMCPReadinessPolicyHistoryCLI(t *testing.T) {
	revision := "7e53717e-9b82-4ea4-a594-4a1d8aa2a8bb"
	current := "cb136aee-df09-4663-9f09-810fd6e4d6a2"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Path {
		case "/v1/apps/test-mcp/mcp-readiness-policy/history":
			if r.URL.Query().Get("scope") != "staging" || r.URL.Query().Get("before_revision") != revision || r.URL.Query().Get("limit") != "1" {
				t.Error("history lost cursor/scope")
			}
			writeJSONTest(w, api.MCPReadinessPolicyHistory{Policies: []api.MCPReadinessPolicy{}})
		case "/v1/apps/test-mcp/mcp-readiness-policy/diff":
			if r.URL.Query().Get("scope") != "staging" || r.URL.Query().Get("from_revision") != revision || r.URL.Query().Get("to_revision") != current {
				t.Error("diff lost revision/scope")
			}
			writeJSONTest(w, api.MCPReadinessPolicyDiff{Scope: "staging", Changes: []api.MCPReadinessPolicyChange{}})
		case "/v1/apps/test-mcp/mcp-readiness-policy/history/" + revision + "/restore":
			var request api.RestoreMCPReadinessPolicyRequest
			if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&request) != nil || request.Scope != "staging" || request.ExpectedRevision != current || request.Reason != "reviewed restore" {
				t.Error("restore lost audit or CAS")
			}
			writeJSONTest(w, api.MCPReadinessPolicy{Scope: "staging", RestoredFrom: revision})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "PLATFORM_ONLY")
	var output bytes.Buffer
	oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
	osStdout, osStderr, jsonOutput = &output, &output, true
	defer func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON }()
	for _, args := range [][]string{
		{"readiness", "policy", "history", "--app", "test-mcp", "--scope", "staging", "--before-revision", revision, "--limit", "1"},
		{"readiness", "policy", "diff", "--app", "test-mcp", "--scope", "staging", "--from-revision", revision, "--to-revision", current},
		{"readiness", "policy", "restore", "--app", "test-mcp", "--scope", "staging", "--revision", revision, "--expected-revision", current, "--reason", "reviewed restore"},
	} {
		if code := cmdMCP(args); code != 0 {
			t.Fatal(code, output.String())
		}
	}
	if calls != 3 {
		t.Fatal("missing command calls", calls)
	}
}
