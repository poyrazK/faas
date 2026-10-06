// adr: 582 — operator diagnostics preserve evidence and bounded pagination.
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

func TestCmdPostgresDiagnostics(t *testing.T) {
	const account = "00000000-0000-0000-0000-000000000001"
	const cursor = "00000000-0000-0000-0000-000000000002"
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.URL.Path != "/v1/admin/managed-postgres/accounting/"+account || r.URL.Query().Get("after") != cursor || r.URL.Query().Get("limit") != "2" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		_, _ = w.Write([]byte(`{"account_id":"` + account + `","evaluated_at":"2026-10-05T12:00:00Z","policy_enabled":true,"window_seconds":3600,"next_cursor":"` + cursor + `","items":[{"database_id":"` + cursor + `","name":"orders","state":"deleted","accounting_required":true,"identity_known":false,"accounting_database_id":"` + cursor + `","blocking":true,"reasons":["legacy_identity_unknown"],"collected_window_seconds":0,"provider_resource_id":"private-upstream","connection_url":"private-password"}]}`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	prevOut, prevJSON := osStdout, jsonOutput
	t.Cleanup(func() { osStdout, jsonOutput = prevOut, prevJSON })
	for _, asJSON := range []bool{false, true} {
		var out bytes.Buffer
		osStdout, jsonOutput = &out, asJSON
		if code := cmdPostgres([]string{"diagnostics", account, "--after", cursor, "--limit", "2"}); code != 0 {
			t.Fatalf("exit=%d", code)
		}
		if strings.Contains(out.String(), "private-") {
			t.Fatal("private provider material rendered")
		}
		if asJSON {
			var page api.ManagedPostgresAccountingDiagnosticsResponse
			if err := json.Unmarshal(out.Bytes(), &page); err != nil || len(page.Items) != 1 || page.Items[0].Reasons[0] != "legacy_identity_unknown" || page.NextCursor != cursor {
				t.Fatalf("JSON: %s %v", out.String(), err)
			}
		} else {
			for _, text := range []string{"legacy_identity_unknown", "blocking=true", "accounting_root:", "next_cursor:"} {
				if !strings.Contains(out.String(), text) {
					t.Fatalf("missing %q: %s", text, out.String())
				}
			}
		}
	}
	for _, args := range [][]string{{"bad"}, {account, "--limit", "0"}, {account, "--limit", "101"}, {account, "--after", "bad"}} {
		if code := cmdPostgresDiagnostics(args); code == 0 {
			t.Fatal("invalid pagination accepted")
		}
	}
	if calls != 2 {
		t.Fatalf("invalid input reached API: %d calls", calls)
	}
}
