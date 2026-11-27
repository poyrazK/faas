package main_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dbguest"
)

func TestPostgresDiagnoseShowsConnectionCapacityAndLockBlockers(t *testing.T) {
	server := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/capabilities" {
			_, _ = w.Write([]byte(`{"registry_version":1,"plan":"pro","capabilities":[],"hidden_capabilities":[]}`))
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/v1/postgres/databases/"+restoreSwitchDB+"/diagnostics" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(api.ManagedPostgresDiagnostics{DatabaseID: restoreSwitchDB, Diagnostics: dbguest.Diagnostics{
			State: dbguest.DiagnosticsAvailable, Sample: &dbguest.ActivitySample{
				SampledAt:   time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC),
				Connections: 9, ConnectionLimit: 9, ActiveTransactions: 1, IdleInTransaction: 1,
				OldestIdleTransactionMS: 33000, LockWaits: 1,
				BlockedSessions: []dbguest.LockWait{{PID: 51, BlockingPIDs: []int32{42}}},
			},
		}})
	})
	code, stdout, stderr := runSwitchCLI(t, server, "postgres", "diagnose", restoreSwitchDB)
	for _, want := range []string{"9 / 9 ordinary slots (at or above capacity)", "PID 51 blocked by PID 42", "oldest 33s"} {
		if code != 0 || !strings.Contains(stdout, want) {
			t.Fatalf("exit %d, want %q, stdout %q, stderr %q", code, want, stdout, stderr)
		}
	}
}
