package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

type configCLIRequest struct {
	method, path, query string
	body                string
}

// configCLIServer fakes the ADR-1451 endpoints and records every request.
func configCLIServer(t *testing.T, guard api.ConfigGuard) (*[]configCLIRequest, func()) {
	t.Helper()
	var mu sync.Mutex
	seen := []configCLIRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(r.Body)
		mu.Lock()
		seen = append(seen, configCLIRequest{r.Method, r.URL.Path, r.URL.RawQuery, buf.String()})
		mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/my-api/config/revisions":
			main.WriteJSONTest(w, api.ListConfigRevisionsResponse{Guard: &guard, Revisions: []api.ConfigRevision{
				{Revision: 4, Source: "config.guard_restore", RestoredRevision: 2, Actor: "gregale:config-guard", CreatedAt: "2026-10-17T10:05:00Z"},
				{Revision: 3, Source: "env.set", Key: "DATABASE_URL", Actor: "dev@example.com", CreatedAt: "2026-10-17T10:00:00Z", GuardStatus: "restored"},
			}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/my-api/config/guard":
			main.WriteJSONTest(w, guard)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/my-api/config/rollback":
			main.WriteJSONTest(w, api.ConfigRollbackResponse{RestoredRevision: 2, Set: []string{"DATABASE_URL"}, Deleted: []string{},
				SecretsNotRestored: []api.ConfigSecretDifference{{Key: "TOKEN", TargetVersion: 1, CurrentVersion: 2}}})
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/v1/apps/my-api/secrets/"):
			main.WriteJSONTest(w, map[string]string{"key": strings.TrimPrefix(r.URL.Path, "/v1/apps/my-api/secrets/")})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/my-api/secrets":
			main.WriteJSONTest(w, api.AppSecretListResponse{})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_config")
	return &seen, srv.Close
}

func captureConfigCLI(t *testing.T, run func() int) (string, int) {
	t.Helper()
	var stdout bytes.Buffer
	old := (*main.OsStdout)
	(*main.OsStdout) = &stdout
	defer func() { (*main.OsStdout) = old }()
	code := run()
	return stdout.String(), code
}

func TestEnvHistoryShowsRevisionsAndTheGuardVerdict(t *testing.T) {
	guard := api.ConfigGuard{ID: "g1", Status: "restored", BaseRevision: 2, HeadRevision: 3,
		Decision: &api.ConfigGuardDecision{Rule: "fail_first_wake_5xx", Summary: "5xx after the config change (new config 8/8, 100.0%; old config 1/120, 0.8%)"}}
	_, done := configCLIServer(t, guard)
	defer done()
	out, code := captureConfigCLI(t, func() int { return main.CmdEnv([]string{"history", "--app", "my-api"}) })
	if code != 0 {
		t.Fatalf("env history exit = %d\n%s", code, out)
	}
	for _, want := range []string{"Config guard: restored revision 2 after 5xx after the config change", "guard restored 2", "env.set DATABASE_URL", "restored", "gregale env rollback <rev>"} {
		if !strings.Contains(out, want) {
			t.Fatalf("env history output lacks %q:\n%s", want, out)
		}
	}
}

func TestEnvRollbackSendsTheRevisionAndGuardOptOut(t *testing.T) {
	seen, done := configCLIServer(t, api.ConfigGuard{})
	defer done()
	out, code := captureConfigCLI(t, func() int {
		return main.CmdEnv([]string{"rollback", "2", "--app", "my-api", "--scope", "staging", "--yes", "--no-guard"})
	})
	if code != 0 {
		t.Fatalf("env rollback exit = %d\n%s", code, out)
	}
	var post configCLIRequest
	for _, r := range *seen {
		if r.method == http.MethodPost {
			post = r
		}
	}
	if !strings.Contains(post.query, "scope=staging") || !strings.Contains(post.query, "guard=false") {
		t.Fatalf("rollback query = %q, want scope=staging and guard=false", post.query)
	}
	var body api.ConfigRollbackRequest
	if err := json.Unmarshal([]byte(post.body), &body); err != nil || body.Revision != 2 {
		t.Fatalf("rollback body = %q, want revision 2", post.body)
	}
	if !strings.Contains(out, "Restored variables of revision 2") || !strings.Contains(out, "secret TOKEN not restored") {
		t.Fatalf("env rollback output:\n%s", out)
	}
}

func TestEnvRollbackRejectsABadRevision(t *testing.T) {
	_, done := configCLIServer(t, api.ConfigGuard{})
	defer done()
	if _, code := captureConfigCLI(t, func() int { return main.CmdEnv([]string{"rollback", "zero", "--app", "my-api", "--yes"}) }); code == 0 {
		t.Fatal("env rollback accepted a non-numeric revision")
	}
}

func TestSecretsSetNoGuardAndGuardNotice(t *testing.T) {
	guard := api.ConfigGuard{ID: "g1", Status: "watching", LastChangeAt: time.Now().UTC().Format(time.RFC3339Nano)}
	seen, done := configCLIServer(t, guard)
	defer done()
	out, code := captureConfigCLI(t, func() int { return main.CmdSecrets([]string{"set", "--app", "my-api", "API_KEY=v1"}) })
	if code != 0 || !strings.Contains(out, "Config guard: if the app starts failing") {
		t.Fatalf("secrets set exit=%d, want the guard notice:\n%s", code, out)
	}
	*seen = (*seen)[:0]
	if _, code := captureConfigCLI(t, func() int { return main.CmdSecrets([]string{"set", "--app", "my-api", "--no-guard", "API_KEY=v2"}) }); code != 0 {
		t.Fatalf("secrets set --no-guard exit = %d", code)
	}
	for _, r := range *seen {
		if r.method == http.MethodPut && !strings.Contains(r.query, "guard=false") {
			t.Fatalf("--no-guard PUT query = %q, want guard=false", r.query)
		}
	}
}
