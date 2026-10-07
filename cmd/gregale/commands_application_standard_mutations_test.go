package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestOrgStandardMutationCLI(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "fixture")
	app, id := uuid.NewString(), uuid.NewString()
	base := "/v1/orgs/acme/application-standard-enrollments/" + app
	for _, tc := range []struct {
		name, method, path, body string
		args                     []string
		response                 any
	}{
		{"local", http.MethodPut, base + "/local-intent", `{"expected_revision":1,"settings":{},"additional_log_destinations":[]}`, []string{"local-intent"}, api.ApplicationStandardEnrollment{AppID: app, State: "pending", DesiredRevision: 2, PersistedRevision: 1}},
		{"approve", http.MethodPost, base + "/exceptions", `{"expected_revision":2,"standard_id":"` + id + `","version":1,"field":"require_signed","value":false,"reason":"Maintenance","expires_at":"2026-10-05T12:00:00Z"}`, []string{"exceptions", "approve"}, api.ApplicationStandardException{ID: id, Status: "active", Value: json.RawMessage(`false`)}},
		{"revoke", http.MethodPost, base + "/exceptions/" + id + "/revoke", `{"expected_revision":3}`, []string{"exceptions", "revoke", "--id", id}, api.ApplicationStandardException{ID: id, Status: "revoked"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "mutation.json")
			if err := os.WriteFile(file, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			called := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called++
				if r.Method != tc.method || r.URL.RequestURI() != tc.path || r.Header.Get("Authorization") != "Bearer fixture" {
					t.Errorf("mutation request: %s %s", r.Method, r.URL.RequestURI())
				}
				var got, want any
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Error(err)
				}
				if err := json.Unmarshal([]byte(tc.body), &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("mutation body changed: %+v", got)
				}
				writeJSONTestStatus(w, http.StatusOK, tc.response)
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			stdout, restore := captureStdout(t)
			defer restore()
			args := append([]string{"standards"}, tc.args...)
			args = append(args, "--org", "acme", "--app", app, "--file", file)
			if code := cmdOrgs(args); code != 0 {
				t.Fatalf("mutation CLI exit: %d", code)
			}
			if called != 1 || !json.Valid([]byte(stdout.String())) {
				t.Fatalf("mutation output %s; calls=%d", stdout.String(), called)
			}
		})
	}
}

func TestOrgStandardMutationCLIRefusalsBeforeNetwork(t *testing.T) {
	id := uuid.NewString()
	for _, tc := range []struct {
		action string
		args   []string
	}{
		{"local-intent", nil}, {"local-intent", []string{"--org", "acme", "--app", id}}, {"approve", []string{"--org", "acme", "--app", "bad", "--file", "body.json"}}, {"revoke", []string{"--org", "acme", "--app", id, "--file", "body.json"}}, {"revoke", []string{"--org", "acme", "--app", id, "--id", uuid.Nil.String(), "--file", "body.json"}}, {"invalid", nil},
	} {
		if _, err := parseStandardMutationCLI(tc.action, tc.args); err == nil {
			t.Fatalf("accepted invalid mutation arguments: %+v", tc)
		}
	}
	t.Setenv("FAAS_TOKEN", "fixture")
	called := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called++ }))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	file := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(file, []byte(`{"expected_revision":1,"settings":{},"additional_log_destinations":null}`), 0600); err != nil {
		t.Fatal(err)
	}
	if code := cmdOrgs([]string{"standards", "local-intent", "--org", "acme", "--app", id, "--file", file}); code == 0 || called != 0 {
		t.Fatalf("invalid JSON reached API: exit=%d calls=%d", code, called)
	}
}
