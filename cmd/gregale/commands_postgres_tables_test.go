package main_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

func TestCmdPostgresTablesReadsAndRendersTableHealth(t *testing.T) {
	const id = "33333333-3333-4333-8333-333333333333"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/postgres/databases/"+id+"/tables" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			http.NotFound(w, r)
			return
		}
		if q := r.URL.Query(); q.Get("sort") != "dead-rows" || q.Get("limit") != "1" {
			t.Errorf("unexpected table options %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"database_id":"` + id + `","state":"available","sample":{"sampled_at":"2026-11-24T08:00:00Z","has_more":true,"tables":[{"schema":"public","name":"orders\t\n\u001b","table_bytes":4096,"index_bytes":8192,"estimated_dead_rows":160,"changes_since_analyze":416,"xid_age":112,"last_analyze_at":"2026-11-24T07:55:00Z"}]}}`))
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fixture")
	previousOut, previousJSON := *main.OsStdout, *main.JsonOutput
	t.Cleanup(func() { *main.OsStdout, *main.JsonOutput = previousOut, previousJSON })
	var out bytes.Buffer
	*main.OsStdout, *main.JsonOutput = &out, false
	if code := main.CmdPostgres([]string{"tables", id, "--sort", "dead-rows", "--limit", "1"}); code != 0 {
		t.Fatal("postgres tables exit code", code)
	}
	for _, expected := range []string{`"public"."orders\t\n\x1b"`, "EST. DEAD ROWS", "160", "416", "More tables are available"} {
		if !strings.Contains(out.String(), expected) {
			t.Errorf("output missing %q: %s", expected, &out)
		}
	}
	if strings.ContainsRune(out.String(), '\x1b') {
		t.Errorf("output contains terminal control character: %q", out.String())
	}
}
