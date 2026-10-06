// adr: 623 — stable resize UUID and safe CLI output.
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

func TestCmdPostgresResizeAndProgress(t *testing.T) {
	const id = "33333333-3333-4333-8333-333333333333"
	posts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/databases":
			_, _ = w.Write([]byte(`{"items":[{"id":"database","name":"orders","state":"ready"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/databases/database":
			_, _ = w.Write([]byte(`{"id":"database","name":"orders","state":"ready"}`))
		default:
			if r.Method == http.MethodPost {
				posts++
				if r.URL.Path != "/v1/postgres/databases/database/resize" {
					t.Error("resize route", r.URL.Path)
				}
				var body api.ResizeManagedPostgresDatabaseRequest
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.RequestID != id || body.ServiceClass != "burstable" {
					t.Error("lost request UUID", body, err)
				}
				w.WriteHeader(202)
			} else if r.Method != http.MethodGet || r.URL.Path != "/v1/postgres/databases/database/resizes/"+id {
				t.Error("status route", r.URL.Path)
			}
			_, _ = w.Write([]byte(`{"id":"` + id + `","database_id":"database","from_class":"development","target_class":"burstable","generation":2,"state":"pending","connection_interruption_expected":true,"password":"PRIVATE_PASSWORD"}`))
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fixture")
	priorOut, priorJSON := osStdout, jsonOutput
	t.Cleanup(func() { osStdout, jsonOutput = priorOut, priorJSON })
	for _, asJSON := range []bool{false, true} {
		var out bytes.Buffer
		osStdout, jsonOutput = &out, asJSON
		if code := cmdPostgres([]string{"resize", "orders", "--class", "burstable", "--request-id", id}); code != 0 {
			t.Fatal("resize", code)
		}
		if strings.Contains(out.String(), "PRIVATE_") {
			t.Fatal("private data leaked")
		}
		out.Reset()
		if code := cmdPostgres([]string{"resize-status", "orders", id}); code != 0 {
			t.Fatal("progress", code)
		}
		if !asJSON && !strings.Contains(out.String(), "disconnect") {
			t.Fatal("omitted interruption notice")
		}
	}
	if posts != 2 {
		t.Fatal("request count", posts)
	}
	if code := cmdPostgres([]string{"resize", "orders", "--class", "burstable"}); code == 0 || posts != 2 {
		t.Fatal("accepted missing durable UUID")
	}
}
