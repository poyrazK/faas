// adr: 1027 — the upgrade command keeps the request UUID stable and says
// what a failed upgrade leaves behind.
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdPostgresUpgradeAndProgress(t *testing.T) {
	const id = "44444444-4444-4444-8444-444444444444"
	posts := 0
	state := `"pending"`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/databases":
			_, _ = w.Write([]byte(`{"items":[{"id":"database","name":"orders","state":"ready"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/databases/database":
			_, _ = w.Write([]byte(`{"id":"database","name":"orders","state":"ready"}`))
		default:
			if r.Method == http.MethodPost {
				posts++
				var body api.UpgradeManagedPostgresDatabaseRequest
				if r.URL.Path != "/v1/postgres/databases/database/upgrade" || json.NewDecoder(r.Body).Decode(&body) != nil || body.RequestID != id || body.PostgresMajor != 17 {
					t.Error("upgrade request", r.URL.Path, body)
				}
				w.WriteHeader(202)
			} else if r.URL.Path != "/v1/postgres/databases/database/upgrades/"+id {
				t.Error("status route", r.URL.Path)
			}
			_, _ = w.Write([]byte(`{"id":"` + id + `","database_id":"database","from_postgres_major":14,"target_postgres_major":17,"generation":2,"state":` + state +
				`,"connection_interruption_expected":true,"last_error_code":"upgrade_refused"}`))
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fixture")
	priorOut, priorJSON := osStdout, jsonOutput
	t.Cleanup(func() { osStdout, jsonOutput = priorOut, priorJSON })
	var out bytes.Buffer
	osStdout, jsonOutput = &out, false
	if code := cmdPostgres([]string{"upgrade", "orders", "--major", "17", "--request-id", id}); code != 0 || !strings.Contains(out.String(), "14 → 17") ||
		!strings.Contains(out.String(), "disconnect once") {
		t.Fatal("upgrade", code, out.String())
	}
	state = `"failed"`
	out.Reset()
	if code := cmdPostgres([]string{"upgrade-status", "orders", id}); code != 0 || !strings.Contains(out.String(), "stays on 14") {
		t.Fatal("status", code, out.String())
	}
	if code := cmdPostgres([]string{"upgrade", "orders", "--major", "17"}); code == 0 || posts != 1 {
		t.Fatal("accepted a missing durable UUID")
	}
	if code := cmdPostgres([]string{"upgrade", "orders", "--request-id", id}); code == 0 || posts != 1 {
		t.Fatal("accepted a missing major")
	}
}
