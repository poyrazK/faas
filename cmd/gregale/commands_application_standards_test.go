package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestOrgStandardsPublicationCLI(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")
	definition := []byte(`{"require_signed":{"mode":"mandatory","value":true}}`)
	path := filepath.Join(t.TempDir(), "standard.json")
	if err := os.WriteFile(path, definition, 0600); err != nil {
		t.Fatal(err)
	}
	called := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		if r.Method != http.MethodPost || r.URL.Path != "/v1/orgs/acme/application-standards/production-baseline/versions" {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		var request api.CreateApplicationStandardVersionRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.ExpectedVersion != 0 || request.Description != "Production baseline" || string(request.Definition) != string(definition) {
			t.Errorf("body %+v", request)
		}
		writeJSONTestStatus(w, http.StatusCreated, api.ApplicationStandardVersion{Slug: "production-baseline", Version: 1})
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	if code := cmdOrgs([]string{"standards", "publish", "--org", "acme", "--standard", "production-baseline", "--file", path, "--expected-version", "0", "--description", "Production baseline"}); code != 0 {
		t.Fatalf("CLI exit %d", code)
	}
	if called != 1 {
		t.Fatalf("requests %d", called)
	}
}

func TestOrgStandardsCLIRejectsUnsafePublication(t *testing.T) {
	for _, args := range [][]string{
		{"publish", "--org", "acme", "--standard", "baseline", "--file", "standard.json"},
		{"publish", "--org", "acme", "--standard", "baseline", "--file", "standard.json", "--expected-version", "-1"},
		{"show", "--org", "acme", "--standard", "baseline", "--version", "-1"},
		{"list", "--org", "acme", "--limit", "101"},
	} {
		if _, err := parseStandardCLI(args[0], args[1:]); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestOrgStandardsResourceCLI(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")
	for _, test := range []struct{ kind, path, input string }{
		{"destinations", "log-destinations", `{"name":"Central","kind":"http_json","target_url":"https://logs.example.com/ingest","auth_header":"Authorization: Bearer example"}`},
		{"publishers", "publishers", `{"name":"CI","public_key_der":"cHVibGlj"}`},
	} {
		t.Run(test.kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "resource.json")
			if err := os.WriteFile(path, []byte(test.input), 0600); err != nil {
				t.Fatal(err)
			}
			called := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called++
				if r.Method != http.MethodPost || r.URL.Path != "/v1/orgs/acme/application-standard-"+test.path {
					t.Errorf("route %s %s", r.Method, r.URL.Path)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["name"] == "" {
					t.Error("missing name")
				}
				writeJSONTestStatus(w, http.StatusCreated, map[string]any{"id": "00000000-0000-4000-8000-000000000001"})
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			if code := cmdOrgs([]string{"standards", test.kind, "create", "--org", "acme", "--file", path}); code != 0 {
				t.Fatalf("exit %d", code)
			}
			if called != 1 {
				t.Fatalf("requests %d", called)
			}
		})
	}
}
