package apidserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectReleaseSecretsHTTPGuards(t *testing.T) {
	for _, tc := range []struct {
		name, slug, body string
		scopes           []string
		enabled          bool
		want             int
	}{
		{"disabled", "project", `{"request_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","workloads":{"api":{"TOKEN":"private"}}}`, api.ScopesAdminOnly, false, 503},
		{"read_only", "project", `{}`, []string{api.ScopeAppsRead}, true, 403},
		{"unknown_field", "project", `{"request_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","workloads":{},"force":true}`, api.ScopesAdminOnly, true, 400},
		{"foreign_project", "missing", `{"request_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","workloads":{"api":{"TOKEN":"private"}}}`, api.ScopesAdminOnly, true, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, store, acct, key := projectSecretsTestServer(t, tc.scopes)
			s.WithProjectReleaseSecretsEnabled(tc.enabled)
			// Project access resolves the project before route guards run.
			if _, err := store.MemStore.CreateProject(t.Context(), state.Project{AccountID: acct.ID, Slug: "project"}); err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodPost, "/v1/projects/"+tc.slug+"/release-secrets", strings.NewReader(tc.body))
			r.Header.Set("Authorization", "Bearer "+key)
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			s.handler().ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("unexpected status %d", w.Code)
			}
			if strings.Contains(w.Body.String(), "private") {
				t.Fatal("plaintext leaked into problem response")
			}
		})
	}
}
func TestProjectReleaseSecretsHTTPStaging(t *testing.T) {
	s, store, acct, key := projectSecretsTestServer(t, api.ScopesAdminOnly)
	s.WithProjectReleaseSecretsEnabled(true)
	project, err := store.MemStore.CreateProject(t.Context(), state.Project{AccountID: acct.ID, Slug: "project"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.MemStore.CreateApp(t.Context(), state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "project-api", WorkloadName: "api"})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"request_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","workloads":{"api":{"TOKEN":"private-http-value"}}}`
	var first api.ProjectReleaseSecretStageResponse
	for i := range 2 {
		r := httptest.NewRequest(http.MethodPost, "/v1/projects/project/release-secrets", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+key)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("staging status=%d", w.Code)
		}
		if strings.Contains(w.Body.String(), "private-http-value") {
			t.Fatal("staging response leaked plaintext")
		}
		var response api.ProjectReleaseSecretStageResponse
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if !api.ValidReleaseSecretVersionID(response.Versions["api"]["TOKEN"]) {
			t.Fatal("missing staged version")
		}
		if i == 0 {
			first = response
		} else if first.Versions["api"]["TOKEN"] != response.Versions["api"]["TOKEN"] {
			t.Fatal("replay changed secret version")
		}
	}
}
