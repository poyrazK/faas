package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdPostgresUpgradeRehearsalStartsAndReads(t *testing.T) {
	const database = "22222222-2222-4222-8222-222222222222"
	const request = "44444444-4444-4444-8444-444444444444"
	const rehearsal = "66666666-6666-4666-8666-666666666666"
	const base = "/v1/postgres/databases/" + database + "/upgrade-rehearsals"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out := api.ManagedPostgresUpgradeRehearsal{
			DatabaseID: database, RehearsalID: rehearsal, RequestID: request, SourceMajor: 16, TargetMajor: 18,
			StartedAt: time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC),
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/databases":
			_, _ = w.Write([]byte(`{"items":[{"id":"` + database + `","name":"orders"}]}`))
			return
		case r.Method == http.MethodPost && r.URL.Path == base:
			var body api.RequestManagedPostgresUpgradeRehearsal
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.RequestID != request || body.PostgresMajor != 18 {
				t.Errorf("start request: %+v, %v", body, err)
			}
			w.WriteHeader(http.StatusAccepted)
			out.Outcome = "queued"
		case r.Method == http.MethodGet && r.URL.Path == base+"/"+rehearsal:
			out.Outcome, out.FailedPhase, out.ErrorCode = "failed", "restore", "missing_wal"
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer server.Close()

	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fixture")
	oldOut, oldJSON := *main.OsStdout, *main.JsonOutput
	t.Cleanup(func() { *main.OsStdout, *main.JsonOutput = oldOut, oldJSON })
	var output bytes.Buffer
	*main.OsStdout, *main.JsonOutput = &output, false
	if code := main.CmdPostgres([]string{"upgrade-rehearsal", "orders", "--major", "18", "--request-id", request}); code != 0 ||
		!strings.Contains(output.String(), rehearsal+" (queued)") || !strings.Contains(output.String(), request) {
		t.Fatalf("start returned %d, %s", code, &output)
	}
	output.Reset()
	if code := main.CmdPostgres([]string{"upgrade-rehearsal", database, "--rehearsal-id", rehearsal}); code != 0 ||
		!strings.Contains(output.String(), rehearsal+" (failed)") || !strings.Contains(output.String(), "missing wal") || !strings.Contains(output.String(), "Failed phase: restore") {
		t.Fatalf("read returned %d, %s", code, &output)
	}
}
