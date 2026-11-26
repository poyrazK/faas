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
	"github.com/onebox-faas/faas/pkg/dbguest"
)

func TestCmdPostgresAutovacuumReadAndSet(t *testing.T) {
	const id = "44444444-4444-4444-8444-444444444444"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/postgres/databases/"+id+"/autovacuum" {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		state, profile := dbguest.AutovacuumApplied, dbguest.AutovacuumDefault
		var effective *dbguest.AutovacuumSettings
		switch r.Method {
		case http.MethodGet:
			settings, _ := profile.Settings()
			effective = &settings
		case http.MethodPut:
			var body api.SetManagedPostgresAutovacuumRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Profile != dbguest.AutovacuumHighChurn {
				t.Errorf("unexpected profile request %+v, %v", body, err)
			}
			state, profile = dbguest.DiagnosticsParked, body.Profile
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
		_ = json.NewEncoder(w).Encode(api.ManagedPostgresAutovacuum{DatabaseID: id, AutovacuumStatus: dbguest.AutovacuumStatus{
			Profile: profile, State: state, Effective: effective,
		}})
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fixture")
	oldOut, oldJSON := *main.OsStdout, *main.JsonOutput
	t.Cleanup(func() { *main.OsStdout, *main.JsonOutput = oldOut, oldJSON })
	var out bytes.Buffer
	*main.OsStdout, *main.JsonOutput = &out, false
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"autovacuum", id}, "vacuum 0.2, analyze 0.1"},
		{[]string{"autovacuum", id, "--profile", "high-churn"}, "high-churn (parked)"},
	} {
		out.Reset()
		if code := main.CmdPostgres(tc.args); code != 0 || !strings.Contains(out.String(), tc.want) {
			t.Fatalf("command %v returned %d, %q", tc.args, code, &out)
		}
	}
}
