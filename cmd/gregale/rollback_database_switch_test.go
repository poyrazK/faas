package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// switchServer answers a rollback that restores the database
// (ADR-20261023181931): unacknowledged requests get the crossing refusal,
// acknowledged ones a switch that then reports each phase in turn.
type switchServer struct {
	mu        sync.Mutex
	rollbacks []api.RollbackRequest
	cancels   int
	phases    []string
	reads     int
}

func (s *switchServer) switchAt(phase string) api.DatabaseSwitch {
	sw := api.DatabaseSwitch{ID: "sw-1", AppID: "app-1", Scope: "default", Phase: phase, FromDeploymentID: "d42", FromRevision: 42,
		TargetDeploymentID: "d41", TargetRevision: 41, UndoneDeploymentIDs: []string{"d42"}, SourceDatabaseID: "db-1",
		SourceDatabaseName: "orders", TargetName: "orders-before-v42", RestorePoint: "2026-10-23T12:03:10Z",
		Drain: api.DatabaseSwitchDrain{Retired: 3, Parked: 1}}
	if phase == "failed" {
		sw.FailureCode, sw.FailureDetail, sw.TargetDatabaseID = "drain_blocked", "1 instances could not be drained.", "db-2"
	}
	return sw
}

func (s *switchServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/shop/rollback":
		var req api.RollbackRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		s.rollbacks = append(s.rollbacks, req)
		if !req.AcknowledgeReleases {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(api.Problem{Status: 409, Code: api.CodeRollbackCrossesRelease, Title: "Rollback crosses release commands",
				CrossedReleases: []api.RollbackCrossedRelease{{DeploymentID: "d42", Revision: 42, Status: "succeeded", StartedAt: "2026-10-23T12:03:10Z"}},
				RestorePoints: []api.ReleaseRestorePoint{{DatabaseID: "db-1", DatabaseName: "orders", PointInTime: "2026-10-23T12:03:10Z"}}})
			return
		}
		sw := s.switchAt("restoring")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d41", DatabaseSwitch: &sw})
	case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/shop/database-switches/sw-1":
		phase := s.phases[min(s.reads, len(s.phases)-1)]
		s.reads++
		_ = json.NewEncoder(w).Encode(s.switchAt(phase))
	case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/shop/database-switches/sw-1/cancel":
		s.cancels++
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(s.switchAt("draining"))
	case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/shop/database-switches":
		_ = json.NewEncoder(w).Encode(api.DatabaseSwitchList{Items: []api.DatabaseSwitch{s.switchAt("completed")}})
	default:
		http.NotFound(w, r)
	}
}

func runSwitchCLI(t *testing.T, s *switchServer, args ...string) (int, string, string) {
	t.Helper()
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	var stdout, stderr bytes.Buffer
	oldOut, oldErr := (*main.OsStdout), (*main.OsStderr)
	(*main.OsStdout), (*main.OsStderr) = &stdout, &stderr
	t.Cleanup(func() { (*main.OsStdout), (*main.OsStderr) = oldOut, oldErr })
	code := main.Run(args)
	return code, stdout.String(), stderr.String()
}

func TestRollbackRestoreDatabasesFollowsTheSwitchToTheEnd(t *testing.T) {
	s := &switchServer{phases: []string{"draining", "switching", "completed"}}
	code, out, errOut := runSwitchCLI(t, s, "rollback", "shop", "--to", "v41", "--restore-databases", "--acknowledge-releases", "--poll-interval", "1ms")
	if code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	if len(s.rollbacks) != 1 || !s.rollbacks[0].RestoreDatabases || !s.rollbacks[0].AcknowledgeReleases ||
		s.rollbacks[0].TargetDeploymentID == nil || *s.rollbacks[0].TargetDeploymentID != "v41" {
		t.Fatalf("rollback requests %+v", s.rollbacks)
	}
	for _, want := range []string{"restoring orders to 2026-10-23T12:03:10Z as orders-before-v42", "draining shop: 3 retired, 1 parked, 0 pending",
		"switching shop to v41 and orders-before-v42", "Rolled back to v41 on orders-before-v42"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
}

func TestRollbackRestoreDatabasesExplainsAndStopsWithoutConfirmation(t *testing.T) {
	s := &switchServer{phases: []string{"completed"}}
	code, _, errOut := runSwitchCLI(t, s, "rollback", "shop", "--to", "v41", "--restore-databases")
	if code == 0 || len(s.rollbacks) != 1 {
		t.Fatalf("exit %d after %d requests; a script without --acknowledge-releases must stop", code, len(s.rollbacks))
	}
	for _, want := range []string{"v42  succeeded", "orders is restored to 2026-10-23T12:03:10Z as a new database",
		"stay only in orders", "shop stops serving while it switches", "--acknowledge-releases"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr lacks %q:\n%s", want, errOut)
		}
	}
}

func TestRollbackRestoreDatabasesReportsAFailedSwitch(t *testing.T) {
	s := &switchServer{phases: []string{"aborting", "failed"}}
	code, _, errOut := runSwitchCLI(t, s, "rollback", "shop", "--restore-databases", "--acknowledge-releases", "--poll-interval", "1ms")
	if code == 0 || !strings.Contains(errOut, "drain_blocked") || !strings.Contains(errOut, "still serves v42 on orders") ||
		!strings.Contains(errOut, "gregale postgres delete db-2") {
		t.Fatalf("exit %d\nstderr:\n%s", code, errOut)
	}
}

func TestRollbackSwitchSubcommands(t *testing.T) {
	s := &switchServer{phases: []string{"draining"}}
	if code, out, errOut := runSwitchCLI(t, s, "rollback", "switch", "shop", "sw-1", "--cancel"); code != 0 || s.cancels != 1 ||
		!strings.Contains(out, "Cancellation requested") {
		t.Fatalf("cancel exit %d cancels %d\n%s%s", code, s.cancels, out, errOut)
	}
	if code, out, _ := runSwitchCLI(t, s, "rollback", "switch", "shop", "sw-1"); code != 0 || !strings.Contains(out, "orders -> orders-before-v42") {
		t.Fatalf("show exit %d\n%s", code, out)
	}
	if code, out, _ := runSwitchCLI(t, s, "rollback", "switches", "shop"); code != 0 || !strings.Contains(out, "sw-1") || !strings.Contains(out, "v42 -> v41") {
		t.Fatalf("list exit %d\n%s", code, out)
	}
}
