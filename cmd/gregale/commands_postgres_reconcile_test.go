// adr: 591 — preview is the default; applying requires reviewed revision evidence.
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

func TestCmdPostgresReconcile(t *testing.T) {
	const account = "00000000-0000-0000-0000-000000000001"
	file := filepath.Join(t.TempDir(), "retained.json")
	sessionFile := filepath.Join(t.TempDir(), "operator-session")
	if err := os.WriteFile(sessionFile, []byte("opaque-session"), 0o600); err != nil {
		t.Fatal(err)
	}
	request := api.ManagedPostgresAccountingReconciliationRequest{ReconciliationID: "00000000-0000-0000-0000-000000000002", DatabaseID: "00000000-0000-0000-0000-000000000003",
		BackendID: "primary", BackendFingerprint: strings.Repeat("a", 64), ProviderResourceID: "retained-id",
		ShutdownAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), ObservedAt: time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC),
		EvidenceReference: "retained/shutdown", EvidenceSHA256: strings.Repeat("a", 64), Reason: "Repair legacy identity"}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost {
			t.Errorf("method=%s", r.Method)
		}
		var body api.ManagedPostgresAccountingReconciliationRequest
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
		want := "/v1/admin/managed-postgres/accounting/" + account + "/reconciliations"
		if !apply {
			want += "/preview"
		}
		if r.URL.Path != want || body.ReconciliationID != request.ReconciliationID || r.Header.Get("Idempotency-Key") == "" {
			t.Errorf("unexpected request=%s %+v", r.URL, body)
		}
		_ = json.NewEncoder(w).Encode(api.ManagedPostgresAccountingReconciliationResult{ReconciliationID: body.ReconciliationID, DatabaseID: body.DatabaseID,
			Revision: strings.Repeat("b", 64), Applied: apply, ShutdownAt: body.ShutdownAt, ObservedAt: body.ObservedAt, RequiresUsageRecovery: true})
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
			args := []string{"reconcile", account, "--file", file}
			if apply {
				args = append(args, "--apply", "--session-file", sessionFile)
			}
			if code := cmdPostgres(args); code != 0 {
				t.Fatalf("exit=%d", code)
			}
			if asJSON {
				var result api.ManagedPostgresAccountingReconciliationResult
				if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Applied != apply {
					t.Fatalf("JSON=%s %v", out.String(), err)
				}
			} else if !strings.Contains(out.String(), "revision:") || !strings.Contains(out.String(), "usage recovery required: true") {
				t.Fatalf("text=%s", out.String())
			}
		}
	}
	if err := os.WriteFile(file, []byte(`{"unexpected":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{account, "--file", file}, {"bad", "--file", file}, {account}, {account, "--file", file, "--apply"}} {
		if cmdPostgresReconcile(args) == 0 {
			t.Fatal("invalid file/arguments accepted")
		}
	}
	if calls != 4 {
		t.Fatalf("invalid input reached server: %d calls", calls)
	}
	request.ExpectedRevision = ""
	valid, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{append(append([]byte{}, valid...), []byte(` {}`)...), append([]byte(strings.Repeat(" ", 32<<10)), valid...)} {
		if err := os.WriteFile(file, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if cmdPostgresReconcile([]string{account, "--file", file}) == 0 {
			t.Fatal("trailing or oversized evidence accepted")
		}
	}
	if err := os.WriteFile(file, valid, 0o600); err != nil {
		t.Fatal(err)
	}
	if cmdPostgresReconcile([]string{account, "--file", file, "--apply", "--session-file", sessionFile}) == 0 {
		t.Fatal("apply without preview revision accepted")
	}
	request.ExpectedRevision = strings.Repeat("b", 64)
	valid, err = json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, valid, 0o600); err != nil {
		t.Fatal(err)
	}
	if cmdPostgresReconcile([]string{account, "--file", file}) == 0 || cmdPostgresReconcile([]string{account, "--file", file, "--apply"}) == 0 {
		t.Fatal("preview containing a revision or apply without session accepted")
	}
	if err := os.Chmod(sessionFile, 0o644); err != nil {
		t.Fatal(err)
	}
	if cmdPostgresReconcile([]string{account, "--file", file, "--apply", "--session-file", sessionFile}) == 0 {
		t.Fatal("nonprivate session file accepted")
	}
	if calls != 4 {
		t.Fatalf("invalid evidence or session reached server: %d calls", calls)
	}
}
