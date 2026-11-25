package main_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPostgresRestoreCheckReportsGapUsingReadRoute(t *testing.T) {
	point := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	const missing = "000000010000000000000003"
	var calls atomic.Int32
	server := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/capabilities" && r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"registry_version":1,"plan":"pro","capabilities":[],"hidden_capabilities":[]}`))
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/v1/postgres/databases/"+restoreSwitchDB+"/restore-check" ||
			r.URL.Query().Get("point_in_time") != point.Format(time.RFC3339) {
			t.Errorf("unexpected restore check request %s %s", r.Method, r.URL)
			http.NotFound(w, r)
			return
		}
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(api.ManagedPostgresRestoreCheck{
			PointInTime: point, Outcome: "wal_gap", Reason: "missing_wal_segment", MissingWALSegment: missing, RecoveryTimeline: 2,
		})
	})
	code, out, errOut := runSwitchCLI(t, server, "postgres", "restore", restoreSwitchDB,
		"--check", "--point-in-time", point.Format(time.RFC3339))
	if code != 0 || !strings.Contains(out, "confirmed WAL gap") || !strings.Contains(out, missing) || !strings.Contains(out, "Recovery timeline: 2") || calls.Load() != 1 {
		t.Fatalf("check exit %d, requests %d\nstdout: %s\nstderr: %s", code, calls.Load(), out, errOut)
	}
	code, _, _ = runSwitchCLI(t, server, "postgres", "restore", restoreSwitchDB,
		"--check", "--name", "orders-copy", "--point-in-time", point.Format(time.RFC3339))
	if code == 0 || calls.Load() != 1 {
		t.Fatalf("check accepted the copy name, exit %d, requests %d", code, calls.Load())
	}
}
