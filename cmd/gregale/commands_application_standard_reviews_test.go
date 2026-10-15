package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestOrgStandardInspectionCLI(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")
	id, app := uuid.NewString(), uuid.NewString()
	req := api.ApplicationStandardReviewRequest{AssignmentID: id, Scope: "organization", ScopeID: id, StandardID: id, AdmissionVersion: 1, ExpectedRevision: 1, Active: false, BatchSize: 2}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "review.json")
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name         string
		args         []string
		path, method string
		response     any
	}{
		{"preview", []string{"reviews", "preview", "--org", "acme", "--file", file}, "/v1/orgs/acme/application-standard-reviews", http.MethodPost, api.ApplicationStandardReview{ID: id, Request: req}},
		{"review", []string{"reviews", "show", "--org", "acme", "--id", id}, "/v1/orgs/acme/application-standard-reviews/" + id, http.MethodGet, api.ApplicationStandardReview{ID: id, Request: req}},
		{"operation", []string{"operation", "--org", "acme", "--id", id}, "/v1/orgs/acme/application-standard-operations/" + id, http.MethodGet, api.ApplicationStandardOperation{ID: id, State: "waiting", Targets: []api.ApplicationStandardOperationTarget{{AppID: app, State: "persisted", DesiredRevision: 2}}}},
		{"exceptions", []string{"exceptions", "--org", "acme", "--app", app, "--after", id, "--limit", "1"}, "/v1/orgs/acme/application-standard-enrollments/" + app + "/exceptions?after=" + id + "&limit=1", http.MethodGet, api.ApplicationStandardExceptionList{Exceptions: []api.ApplicationStandardException{{ID: id, Status: "expired"}}, AsOf: time.Now().UTC()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called++
				if r.Method != tc.method || r.URL.RequestURI() != tc.path {
					t.Errorf("inspection: %s %s", r.Method, r.URL.RequestURI())
				}
				if r.Method == http.MethodPost {
					var got api.ApplicationStandardReviewRequest
					if err := json.NewDecoder(r.Body).Decode(&got); err != nil || got.Active || got.ExpectedRevision != 1 {
						t.Errorf("explicit false request lost: %+v %v", got, err)
					}
				}
				writeJSONTestStatus(w, http.StatusOK, tc.response)
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			stdout, restore := captureStdout(t)
			defer restore()
			if code := cmdOrgs(append([]string{"standards"}, tc.args...)); code != 0 {
				t.Fatalf("CLI exit %d", code)
			}
			if !json.Valid([]byte(stdout.String())) || called != 1 {
				t.Fatalf("CLI response %s; requests %d", stdout.String(), called)
			}
		})
	}
}

func TestOrgStandardInspectionCLIValidation(t *testing.T) {
	for _, tc := range []struct {
		action string
		args   []string
	}{
		{"preview", []string{"--org", "acme"}}, {"show", []string{"--org", "acme", "--id", "bad"}}, {"operation", []string{"--org", "acme", "--id", uuid.Nil.String()}}, {"exceptions", []string{"--org", "acme", "--app", uuid.NewString(), "--limit", "101"}}, {"exceptions", []string{"--org", "acme", "--app", uuid.NewString(), "--after", "bad"}}, {"bad", nil},
	} {
		if _, err := parseStandardInspectionCLI(tc.action, tc.args); err == nil {
			t.Fatalf("accepted %s %v", tc.action, tc.args)
		}
	}
	file := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(file, []byte(`{"active":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readStandardReviewCLI(file); err == nil {
		t.Fatal("accepted incomplete review file")
	}
}
