package main_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

const (
	gatePrevious    = "11111111-1111-4111-8111-111111111111"
	gateReplacement = "22222222-2222-4222-8222-222222222222"
)

type fakeGateStore struct {
	mu       sync.Mutex
	locked   bool
	held     bool
	now      time.Time
	workers  []main.MCPGateWorker
	pending  []main.MCPTaskHandlerRef
	unlocked bool
}

func (s *fakeGateStore) TryLock(context.Context, string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.held {
		return false, nil
	}
	s.locked = true
	return true, nil
}

func (s *fakeGateStore) Unlock(context.Context, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.locked, s.unlocked = false, true
	return nil
}

func (s *fakeGateStore) Now(context.Context) (time.Time, error) { return s.now, nil }

func (s *fakeGateStore) Workers(context.Context, string) ([]main.MCPGateWorker, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.workers), nil
}

func (s *fakeGateStore) PendingHandlers(context.Context, string) ([]main.MCPTaskHandlerRef, error) {
	return s.pending, nil
}

func (s *fakeGateStore) add(worker main.MCPGateWorker) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workers = append(s.workers, worker)
}

func (s *fakeGateStore) remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workers = slices.DeleteFunc(s.workers, func(w main.MCPGateWorker) bool { return w.ID == id })
}

var (
	reportV1 = main.MCPTaskHandlerRef{Name: "build_report", Version: "v1"}
	reportV2 = main.MCPTaskHandlerRef{Name: "build_report", Version: "v2"}
)

func newGateStore() *fakeGateStore {
	t0 := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	return &fakeGateStore{now: t0, pending: []main.MCPTaskHandlerRef{reportV1},
		workers: []main.MCPGateWorker{{ID: gatePrevious, Handlers: []main.MCPTaskHandlerRef{reportV1}, Heartbeat: t0, Fresh: true}}}
}

type gateHooks struct {
	migrated, started, drained int
	replacement                main.MCPGateWorker
	migrateErr                 error
	startIDs                   []string
}

func (h *gateHooks) run(t *testing.T, store *fakeGateStore, checkpoint string) main.MCPGateReport {
	t.Helper()
	return main.RunMCPGate(t.Context(), store, "reports", "release-1", checkpoint, 200*time.Millisecond,
		func(context.Context) error { h.migrated++; return h.migrateErr },
		func(_ context.Context, input map[string]any) ([]string, error) {
			h.started++
			if !slices.Equal(input["previousWorkerIDs"].([]string), []string{gatePrevious}) {
				t.Errorf("start input = %v", input)
			}
			store.add(h.replacement)
			if h.startIDs != nil {
				return h.startIDs, nil
			}
			return []string{h.replacement.ID}, nil
		},
		func(_ context.Context, input map[string]any) error {
			h.drained++
			if !slices.Equal(input["replacementWorkerIDs"].([]string), []string{gateReplacement}) {
				t.Errorf("drain input = %v", input)
			}
			store.remove(gatePrevious)
			return nil
		})
}

func readyReplacement(store *fakeGateStore, handlers ...main.MCPTaskHandlerRef) main.MCPGateWorker {
	return main.MCPGateWorker{ID: gateReplacement, Handlers: handlers, Heartbeat: store.now.Add(time.Second), Fresh: true}
}

// ADR-20261123160000: a Go/Python release drains previous workers only after
// each replacement registers every handler version unfinished Tasks need.
func TestMCPBuiltinGateReleasesWhenReplacementsServePendingTasks(t *testing.T) {
	store := newGateStore()
	checkpoint := filepath.Join(t.TempDir(), "journal.gate")
	hooks := &gateHooks{replacement: readyReplacement(store, reportV1, reportV2)}
	report := hooks.run(t, store, checkpoint)
	if !report.OK || report.Stage != "complete" || !slices.Equal(report.ReplacementWorkerIDs, []string{gateReplacement}) || !report.ExecutorSettled {
		t.Fatalf("report = %+v", report)
	}
	if hooks.migrated != 1 || hooks.started != 1 || hooks.drained != 1 || !store.unlocked || store.locked {
		t.Fatalf("hooks = %+v, lock released = %v", hooks, store.unlocked)
	}
	var saved struct {
		Completed            bool     `json:"completed"`
		ReplacementWorkerIDs []string `json:"replacementWorkerIDs"`
		PreviousWorkerIDs    []string `json:"previousWorkerIDs"`
	}
	body, err := os.ReadFile(checkpoint)
	if err != nil || json.Unmarshal(body, &saved) != nil || !saved.Completed || !slices.Equal(saved.PreviousWorkerIDs, []string{gatePrevious}) {
		t.Fatalf("checkpoint = %s (%v)", body, err)
	}
	// A rerun of a completed release only rechecks readiness.
	again := hooks.run(t, store, checkpoint)
	if !again.OK || hooks.started != 1 || hooks.drained != 1 {
		t.Fatalf("rerun = %+v, hooks = %+v", again, hooks)
	}
}

func TestMCPBuiltinGateRefusesUnsafeReleases(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(*fakeGateStore, *gateHooks)
		stage   string
		started int
	}{
		{"replacement lacks a pending handler version", func(s *fakeGateStore, h *gateHooks) { h.replacement = readyReplacement(s, reportV2) }, "replacement_readiness", 1},
		{"replacement heartbeat predates the release", func(s *fakeGateStore, h *gateHooks) {
			h.replacement = readyReplacement(s, reportV1)
			h.replacement.Heartbeat = s.now.Add(-time.Second)
		}, "replacement_readiness", 1},
		{"replacement is draining", func(s *fakeGateStore, h *gateHooks) {
			h.replacement = readyReplacement(s, reportV1)
			h.replacement.Draining = true
		}, "replacement_readiness", 1},
		{"another release holds the lock", func(s *fakeGateStore, h *gateHooks) { s.held = true }, "release_lock", 0},
		{"migration fails", func(s *fakeGateStore, h *gateHooks) { h.migrateErr = errors.New("password=secret") }, "migrations", 0},
		{"start names a previous worker", func(s *fakeGateStore, h *gateHooks) {
			h.replacement = readyReplacement(s, reportV1)
			h.startIDs = []string{gatePrevious}
		}, "start_replacements", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newGateStore()
			hooks := &gateHooks{}
			tc.prepare(store, hooks)
			report := hooks.run(t, store, filepath.Join(t.TempDir(), "journal.gate"))
			if report.OK || report.Stage != tc.stage || hooks.started != tc.started || hooks.drained != 0 {
				t.Fatalf("report = %+v, hooks = %+v", report, hooks)
			}
			body, _ := json.Marshal(report)
			if strings.Contains(string(body), "secret") {
				t.Fatalf("report leaks hook error: %s", body)
			}
		})
	}
}

// A release interrupted after its replacements started resumes without
// starting another generation.
func TestMCPBuiltinGateResumesFromCheckpoint(t *testing.T) {
	store := newGateStore()
	store.add(readyReplacement(store, reportV1))
	checkpoint := filepath.Join(t.TempDir(), "journal.gate")
	body, _ := json.Marshal(map[string]any{"version": 1, "namespace": "reports", "releaseID": "release-1",
		"previousWorkerIDs": []string{gatePrevious}, "replacementWorkerIDs": []string{gateReplacement}, "startedAt": store.now.Format(time.RFC3339Nano)})
	if err := os.WriteFile(checkpoint, body, 0o600); err != nil {
		t.Fatal(err)
	}
	hooks := &gateHooks{}
	report := hooks.run(t, store, checkpoint)
	if !report.OK || hooks.started != 0 || hooks.drained != 1 {
		t.Fatalf("report = %+v, hooks = %+v", report, hooks)
	}
}

func TestMCPNativeWorkerRuntimeDetection(t *testing.T) {
	for _, tc := range []struct {
		files   []string
		runtime string
		node    bool
	}{
		{[]string{"tasks-release.js", "package.json"}, "node", true},
		{[]string{"go.mod", "task_schema.go", "task_worker_registry.go"}, "go", false},
		{[]string{"tasks.py", "task_schema.py"}, "python", false},
		{[]string{"main.go", "go.mod"}, "", false},
	} {
		dir := t.TempDir()
		for _, name := range tc.files {
			if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		runtime, err := main.MCPNativeWorkerRuntime(dir)
		if runtime != tc.runtime || (err == nil) != (tc.runtime != "") {
			t.Fatalf("%v: runtime %q err %v", tc.files, runtime, err)
		}
		if nodeErr := main.RequireMCPNativeNodeWorker(dir); (nodeErr == nil) != tc.node {
			t.Fatalf("%v: node guard %v", tc.files, nodeErr)
		}
	}
}
