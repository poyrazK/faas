package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdPostgresSessionsAndCancelQuery(t *testing.T) {
	const database = "33333333-3333-4333-8333-333333333333"
	const target = "55555555-5555-4555-8555-555555555555"
	base := "/v1/postgres/databases/" + database
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == base+"/sessions":
			_, _ = w.Write([]byte(`{"database_id":"` + database + `","state":"available","sample":{"sampled_at":"2026-11-24T08:00:00Z","has_more":false,"sessions":[{"pid":717,"state":"active","transaction_age_ms":3250,"application_name":"orders\t\u001b","wait_event_type":"Lock","wait_event":"advisory","target_id":"` + target + `","query":"private-statement"}]}}`))
		case r.Method == http.MethodPost && r.URL.Path == base+"/cancel-query":
			var body api.CancelManagedPostgresQueryRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.TargetID != target {
				t.Errorf("cancel request %+v, %v", body, err)
			}
			_, _ = w.Write([]byte(`{"database_id":"` + database + `","target_id":"` + target + `","outcome":"stale_target"}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fixture")
	previousOut, previousJSON := *main.OsStdout, *main.JsonOutput
	t.Cleanup(func() { *main.OsStdout, *main.JsonOutput = previousOut, previousJSON })
	var out bytes.Buffer
	*main.OsStdout, *main.JsonOutput = &out, false
	if code := main.CmdPostgres([]string{"sessions", database}); code != 0 {
		t.Fatal("sessions exit code", code)
	}
	if strings.ContainsRune(out.String(), '\x1b') || strings.Contains(out.String(), "private-statement") ||
		!strings.Contains(out.String(), "Lock/advisory") || !strings.Contains(out.String(), target) {
		t.Fatalf("unexpected listing %q", out.String())
	}
	out.Reset()
	if code := main.CmdPostgres([]string{"cancel-query", database, target}); code != 1 ||
		!strings.Contains(out.String(), "target is stale") {
		t.Fatalf("cancel returned %d, %q", code, out.String())
	}
}
