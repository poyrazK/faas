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

func TestCmdPostgresRestoreDrillStartsAndReads(t *testing.T) {
	const database = "22222222-2222-4222-8222-222222222222"
	const request = "44444444-4444-4444-8444-444444444444"
	const drill = "66666666-6666-4666-8666-666666666666"
	const base = "/v1/postgres/databases/" + database + "/restore-drills"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		outcome := "failed"
		switch {
		case r.Method == http.MethodPost && r.URL.Path == base:
			var body api.RequestManagedPostgresRestoreDrill
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.RequestID != request {
				t.Errorf("start request: %+v, %v", body, err)
			}
			w.WriteHeader(http.StatusAccepted)
			outcome = "queued"
		case r.Method == http.MethodGet && r.URL.Path == base+"/"+drill:
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"database_id":"` + database + `","drill_id":"` + drill + `","request_id":"` + request +
			`","outcome":"` + outcome + `","started_at":"2026-11-24T09:00:00Z","error_code":"missing_wal"}`))
	}))
	defer server.Close()

	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fixture")
	oldOut, oldJSON := *main.OsStdout, *main.JsonOutput
	t.Cleanup(func() { *main.OsStdout, *main.JsonOutput = oldOut, oldJSON })
	var output bytes.Buffer
	*main.OsStdout, *main.JsonOutput = &output, false
	if code := main.CmdPostgres([]string{"restore-drill", database, "--request-id", request}); code != 0 ||
		!strings.Contains(output.String(), drill+" (queued)") || !strings.Contains(output.String(), request) {
		t.Fatalf("start returned %d, %s", code, &output)
	}
	output.Reset()
	if code := main.CmdPostgres([]string{"restore-drill", database, "--drill-id", drill}); code != 0 ||
		!strings.Contains(output.String(), drill+" (failed)") || !strings.Contains(output.String(), "missing wal") {
		t.Fatalf("read returned %d, %s", code, &output)
	}
}
