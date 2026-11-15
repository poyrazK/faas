package main_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

const restoreSwitchDB = "0b9b1a3e-6a8d-4c39-9d8a-2f9f6a1b0c11"

// restoreSwitchServer answers `gregale postgres restore --switch`
// (ADR-20261023181931 amendment 3): a switch that keeps the live release.
type restoreSwitchServer struct {
	mu       sync.Mutex
	requests []api.CreateDatabaseSwitchRequest
	phases   []string
	reads    int
}

func (s *restoreSwitchServer) switchAt(phase string) api.DatabaseSwitch {
	return api.DatabaseSwitch{ID: "sw-1", AppID: "app-1", Scope: "default", Phase: phase, FromDeploymentID: "d42", FromRevision: 42,
		TargetDeploymentID: "d42", TargetRevision: 42, UndoneDeploymentIDs: []string{}, KeepsRelease: true, SourceDatabaseID: restoreSwitchDB,
		SourceDatabaseName: "orders", TargetName: "orders-0900", RestorePoint: "2026-10-30T09:00:00Z"}
}

func (s *restoreSwitchServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/shop/database-switches":
		var req api.CreateDatabaseSwitchRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		s.requests = append(s.requests, req)
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(s.switchAt("restoring"))
	case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/shop/database-switches/sw-1":
		phase := s.phases[min(s.reads, len(s.phases)-1)]
		s.reads++
		_ = json.NewEncoder(w).Encode(s.switchAt(phase))
	default:
		http.NotFound(w, r)
	}
}

func TestPostgresRestoreSwitchFollowsTheSwitchAndKeepsTheRelease(t *testing.T) {
	s := &restoreSwitchServer{phases: []string{"draining", "switching", "completed"}}
	code, out, errOut := runSwitchCLI(t, s, "postgres", "restore", restoreSwitchDB, "--name", "orders-0900",
		"--point-in-time", "2026-10-30T09:00:00Z", "--switch", "shop", "--yes", "--poll-interval", "1ms")
	if code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	if len(s.requests) != 1 || s.requests[0] != (api.CreateDatabaseSwitchRequest{DatabaseID: restoreSwitchDB, Name: "orders-0900", PointInTime: "2026-10-30T09:00:00Z"}) {
		t.Fatalf("requests %+v", s.requests)
	}
	for _, want := range []string{"restoring orders to 2026-10-30T09:00:00Z as orders-0900", "switching shop to orders-0900",
		"Switched shop to orders-0900. orders keeps every write"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Rolled back") {
		t.Errorf("a restore switch reports a rollback:\n%s", out)
	}
}

func TestPostgresRestoreSwitchNeedsConfirmationInScripts(t *testing.T) {
	s := &restoreSwitchServer{phases: []string{"completed"}}
	code, out, errOut := runSwitchCLI(t, s, "--json", "postgres", "restore", restoreSwitchDB, "--name", "orders-0900",
		"--point-in-time", "2026-10-30T09:00:00Z", "--switch", "shop")
	if code == 0 || len(s.requests) != 0 {
		t.Fatalf("exit %d after %d requests; stdout:\n%s\nstderr:\n%s", code, len(s.requests), out, errOut)
	}
}

func TestPostgresRestoreSwitchFlagsNeedSwitch(t *testing.T) {
	s := &restoreSwitchServer{phases: []string{"completed"}}
	code, _, _ := runSwitchCLI(t, s, "postgres", "restore", restoreSwitchDB, "--name", "orders-0900",
		"--point-in-time", "2026-10-30T09:00:00Z", "--env", "staging")
	if code == 0 || len(s.requests) != 0 {
		t.Fatalf("--env without --switch = exit %d after %d requests", code, len(s.requests))
	}
}
