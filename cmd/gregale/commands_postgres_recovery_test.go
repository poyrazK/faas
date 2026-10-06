package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPostgresRecoveryDisplaysUncertaintyWithoutSecrets(t *testing.T) {
	reads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("recovery mutated", r.Method)
		}
		switch r.URL.Path {
		case "/v1/postgres/databases":
			_, _ = w.Write([]byte(`{"items":[{"id":"database","name":"orders","state":"ready"}]}`))
		case "/v1/postgres/databases/database":
			_, _ = w.Write([]byte(`{"id":"database","name":"orders","state":"ready"}`))
		case "/v1/postgres/databases/database/recovery":
			reads++
			_, _ = w.Write([]byte(`{"database_id":"database","status":"limits_known","fresh":true,"history_bounds_known":false,"retention_seconds":300,"earliest_possible_time":"2026-10-06T10:00:00Z","latest_possible_time":"2026-10-06T10:05:00Z","password":"PRIVATE_PASSWORD"}`))
		default:
			t.Error(r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fixture")
	oldOut, oldJSON := osStdout, jsonOutput
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })
	for _, asJSON := range []bool{false, true} {
		var out bytes.Buffer
		osStdout, jsonOutput = &out, asJSON
		if code := cmdPostgres([]string{"recovery", "orders"}); code != 0 || strings.Contains(out.String(), "PRIVATE_") {
			t.Fatal(code, out.String())
		}
		if !asJSON && !strings.Contains(out.String(), "unconfirmed") {
			t.Fatal("omitted uncertainty", out.String())
		}
	}
	if reads != 2 {
		t.Fatal(reads)
	}
}

func TestPostgresRecoveryHelpDocumentsDatabaseAndUncertainty(t *testing.T) {
	var out bytes.Buffer
	oldOut := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = oldOut })
	if code := run([]string{"postgres", "recovery", "--help"}); code != 0 {
		t.Fatal(code, out.String())
	}
	for _, text := range []string{"<database>", "unconfirmed", "--json"} {
		if !strings.Contains(out.String(), text) {
			t.Fatal("missing help contract", text, out.String())
		}
	}
}
