package apidserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestProjectCanaryControlHTTPGuards(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		scopes     []string
		status     int
	}{
		{"owned_lookup", `{"request_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","expected_generation":1}`, api.ScopesAdminOnly, 404},
		{"read_scope_cannot_write", `{"request_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","expected_generation":1}`, []string{api.ScopeAppsRead}, 403},
		{"unknown_field", `{"request_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","expected_generation":1,"force":true}`, api.ScopesAdminOnly, 400},
		{"trailing_body", `{"request_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","expected_generation":1}{}`, api.ScopesAdminOnly, 400},
		{"missing_generation", `{"request_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`, api.ScopesAdminOnly, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _, key := projectSecretsTestServer(t, tc.scopes)
			r := httptest.NewRequest(http.MethodPost, "/v1/project-submissions/"+strings.Repeat("c", 32)+"/abort", strings.NewReader(tc.body))
			r.Header.Set("Authorization", "Bearer "+key)
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			s.handler().ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status=%d wanted=%d body=%s", w.Code, tc.status, w.Body.String())
			}
		})
	}
}

func TestProjectMigrationContractHTTPGuards(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		scopes     []string
		status     int
	}{
		{"disabled", `{"request_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","expected_generation":1}`, api.ScopesAdminOnly, 503},
		{"read_scope", `{"request_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","expected_generation":1}`, []string{api.ScopeAppsRead}, 403},
		{"forged_evidence", `{"request_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","expected_generation":1,"evidence":[]}`, api.ScopesAdminOnly, 400},
		{"force", `{"request_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","expected_generation":1,"force":true}`, api.ScopesAdminOnly, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _, key := projectSecretsTestServer(t, tc.scopes)
			r := httptest.NewRequest(http.MethodPost, "/v1/project-submissions/"+strings.Repeat("c", 32)+"/contract", strings.NewReader(tc.body))
			r.Header.Set("Authorization", "Bearer "+key)
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			s.handler().ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status=%d want=%d body=%s", w.Code, tc.status, w.Body.String())
			}
		})
	}
}

func TestMigrationRecoveryHTTPGuards(t *testing.T) {
	base := `{"request_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","expected_task_id":"11111111-1111-4111-8111-111111111111","expected_generation":1,"replacement_attempt_id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","database_state_checked":true,"execution_quiescent":true,"evidence_hash":"` + strings.Repeat("c", 64) + `"}`
	for _, test := range []struct {
		name, body string
		scopes     []string
		status     int
	}{
		{"owned_lookup", base, api.ScopesAdminOnly, 404}, {"read_scope", base, []string{api.ScopeAppsRead}, 403},
		{"missing_attestation", strings.Replace(base, `"execution_quiescent":true`, `"execution_quiescent":false`, 1), api.ScopesAdminOnly, 400},
		{"forged_health", strings.TrimSuffix(base, "}") + `,"probes":[]}`, api.ScopesAdminOnly, 400},
		{"force", strings.TrimSuffix(base, "}") + `,"force":true}`, api.ScopesAdminOnly, 400},
		{"trailing", base + `{}`, api.ScopesAdminOnly, 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, _, _, key := projectSecretsTestServer(t, test.scopes)
			r := httptest.NewRequest(http.MethodPost, "/v1/project-submissions/"+strings.Repeat("d", 32)+"/resolve-contract", strings.NewReader(test.body))
			r.Header.Set("Authorization", "Bearer "+key)
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			s.handler().ServeHTTP(w, r)
			if w.Code != test.status {
				t.Fatalf("status=%d want=%d body=%s", w.Code, test.status, w.Body.String())
			}
		})
	}
}
