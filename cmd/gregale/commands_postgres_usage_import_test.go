// adr: 583 — preview is the default; applying requires reviewed revision evidence.
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdPostgresUsageImport(t *testing.T) {
	const account = "00000000-0000-0000-0000-000000000001"
	file := filepath.Join(t.TempDir(), "retained.json")
	sessionFile := filepath.Join(t.TempDir(), "operator-session")
	if err := os.WriteFile(sessionFile, []byte("opaque-session"), 0o600); err != nil {
		t.Fatal(err)
	}
	request := api.ManagedPostgresUsageImportRequest{ImportID: "00000000-0000-0000-0000-000000000002", DatabaseID: "00000000-0000-0000-0000-000000000003", EvidenceReference: "retained/export", EvidenceSHA256: strings.Repeat("a", 64), Reason: "recover gap", Windows: []api.ManagedPostgresUsageImportWindow{{From: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 1, 1, 0, 0, 0, time.UTC), ObservedAt: time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC), Readings: []api.ManagedPostgresUsageImportReading{{Meter: "compute_unit_seconds", Quantity: 60}}}}}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost {
			t.Errorf("method=%s", r.Method)
		}
		var body api.ManagedPostgresUsageImportRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		apply := body.ExpectedRevision != ""
		if apply {
			if r.Header.Get("Authorization") != "" {
				t.Error("session request sent bearer key")
			}
			cookie, err := r.Cookie("faas_sid")
			if err != nil || cookie.Value != "opaque-session" {
				t.Error("operator session cookie missing")
			}
		}
		want := "/v1/admin/managed-postgres/accounting/" + account + "/usage-imports"
		if !apply {
			want += "/preview"
		}
		if r.URL.Path != want || body.ImportID != request.ImportID || r.Header.Get("Idempotency-Key") == "" {
			t.Errorf("unexpected request=%s %+v", r.URL, body)
		}
		_ = json.NewEncoder(w).Encode(api.ManagedPostgresUsageImportResult{ImportID: body.ImportID, DatabaseID: body.DatabaseID, Revision: strings.Repeat("b", 64), Applied: apply, WindowCount: 1, ImportedCostMillicents: 60, CostDeltaMillicents: 60, CollectedFrom: body.Windows[0].From, CollectedUntil: body.Windows[0].To, ObservedAt: body.Windows[0].ObservedAt})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	previousOut, previousJSON := osStdout, jsonOutput
	t.Cleanup(func() { osStdout, jsonOutput = previousOut, previousJSON })
	for _, apply := range []bool{false, true} {
		for _, asJSON := range []bool{false, true} {
			request.ExpectedRevision = ""
			if apply {
				request.ExpectedRevision = strings.Repeat("b", 64)
			}
			data, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, data, 0o600); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			osStdout, jsonOutput = &out, asJSON
			args := []string{"usage-import", account, "--file", file}
			if apply {
				args = append(args, "--apply", "--session-file", sessionFile)
			}
			if code := cmdPostgres(args); code != 0 {
				t.Fatalf("exit=%d", code)
			}
			if asJSON {
				var result api.ManagedPostgresUsageImportResult
				if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Applied != apply {
					t.Fatalf("JSON=%s %v", out.String(), err)
				}
			} else if !strings.Contains(out.String(), "revision:") || !strings.Contains(out.String(), "cost delta: 60 millicents") {
				t.Fatalf("text=%s", out.String())
			}
		}
	}
	if err := os.WriteFile(file, []byte(`{"unexpected":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{account, "--file", file}, {"bad", "--file", file}, {account}, {account, "--file", file, "--apply"}} {
		if cmdPostgresUsageImport(args) == 0 {
			t.Fatal("invalid file/arguments accepted")
		}
	}
	if calls != 4 {
		t.Fatalf("invalid input reached server: %d calls", calls)
	}
}
