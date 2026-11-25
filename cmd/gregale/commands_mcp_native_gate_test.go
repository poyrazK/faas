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
	"github.com/onebox-faas/faas/pkg/api"
)

const (
	gatePreviousDeployment  = "dep-previous"
	gateCandidateDeployment = "dep-candidate"
	gateReplacement         = "22222222-2222-4222-8222-222222222222"
	gateOtherWorker         = "11111111-1111-4111-8111-111111111111"
)

var gateStart = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

// fakeGateStack is the observer's view of one Task namespace: live workers per
// deployment for the build_report handler version unfinished Tasks need.
type fakeGateStack struct {
	mu          sync.Mutex
	observedAt  time.Time
	stale       bool
	truncated   bool
	workers     map[string]int64
	candidate   string
	unpublished bool
}

func newGateStack() *fakeGateStack {
	return &fakeGateStack{observedAt: gateStart.Add(time.Second), workers: map[string]int64{gatePreviousDeployment: 1}}
}

func (s *fakeGateStack) report(context.Context) (api.MCPTaskCompatibilityReportResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	handler := api.MCPTaskHandlerCompatibility{ToolName: "build_report", HandlerVersion: "v1", WaitingTasks: 3}
	for id, n := range s.workers {
		if n > 0 {
			handler.Deployments = append(handler.Deployments, api.MCPTaskHandlerDeploymentWorkers{DeploymentID: id, Workers: n})
			handler.LiveCompatibleWorkers += n
		}
	}
	observed := s.observedAt
	return api.MCPTaskCompatibilityReportResponse{Published: !s.unpublished, Handlers: []api.MCPTaskHandlerCompatibility{handler},
		Truncated: s.truncated, ObservedAt: &observed, FreshnessSeconds: 1, Stale: s.stale}, nil
}

func (s *fakeGateStack) deployments() (main.MCPGateDeployments, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return main.MCPGateDeployments{Candidate: s.candidate, Previous: []string{gatePreviousDeployment}}, nil
}

func (s *fakeGateStack) set(fn func(*fakeGateStack)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s)
}

type gateHooks struct {
	migrated, started, drained int
	candidateWorkers           int64
	migrateErr                 error
	startIDs                   []string
	keepPrevious               bool
}

func (h *gateHooks) run(t *testing.T, stack *fakeGateStack, checkpoint string) main.MCPGateReport {
	t.Helper()
	return main.RunMCPGate(t.Context(), stack.report, stack.deployments, "reports", "release-1", checkpoint, gateStart, 200*time.Millisecond,
		func(context.Context) error { h.migrated++; return h.migrateErr },
		func(_ context.Context, input map[string]any) ([]string, error) {
			h.started++
			if ids, ok := input["previousWorkerIDs"].([]string); !ok || len(ids) != 0 {
				t.Errorf("start input = %v", input)
			}
			stack.set(func(s *fakeGateStack) {
				s.candidate = gateCandidateDeployment
				s.workers[gateCandidateDeployment] = h.candidateWorkers
			})
			if h.startIDs != nil {
				return h.startIDs, nil
			}
			return []string{gateReplacement}, nil
		},
		func(_ context.Context, input map[string]any) error {
			h.drained++
			if !slices.Equal(input["replacementWorkerIDs"].([]string), []string{gateReplacement}) {
				t.Errorf("drain input = %v", input)
			}
			if !h.keepPrevious {
				stack.set(func(s *fakeGateStack) { delete(s.workers, gatePreviousDeployment) })
			}
			return nil
		})
}

// ADR-20261123160000 amendment 1: a Go/Python release drains previous workers
// only after a fresh observer report shows the candidate deployment serving
// every handler version unfinished Tasks need, and completes once the report no
// longer lists the previous deployment.
func TestMCPBuiltinGateReleasesWhenReplacementsServePendingTasks(t *testing.T) {
	stack := newGateStack()
	checkpoint := filepath.Join(t.TempDir(), "journal.gate")
	hooks := &gateHooks{candidateWorkers: 1}
	report := hooks.run(t, stack, checkpoint)
	if !report.OK || report.Stage != "complete" || !slices.Equal(report.ReplacementWorkerIDs, []string{gateReplacement}) || !report.ExecutorSettled {
		t.Fatalf("report = %+v", report)
	}
	if hooks.migrated != 1 || hooks.started != 1 || hooks.drained != 1 {
		t.Fatalf("hooks = %+v", hooks)
	}
	var saved struct {
		Completed            bool     `json:"completed"`
		ReplacementWorkerIDs []string `json:"replacementWorkerIDs"`
		StartedAt            string   `json:"startedAt"`
	}
	body, err := os.ReadFile(checkpoint)
	if err != nil || json.Unmarshal(body, &saved) != nil || !saved.Completed || saved.StartedAt != gateStart.Format(time.RFC3339Nano) {
		t.Fatalf("checkpoint = %s (%v)", body, err)
	}
	// A rerun of a completed release only rechecks readiness.
	again := hooks.run(t, stack, checkpoint)
	if !again.OK || hooks.started != 1 || hooks.drained != 1 {
		t.Fatalf("rerun = %+v, hooks = %+v", again, hooks)
	}
}

func TestMCPBuiltinGateRefusesUnsafeReleases(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(*fakeGateStack, *gateHooks)
		stage   string
		started int
		drained int
	}{
		{"candidate lacks a pending handler version", func(s *fakeGateStack, h *gateHooks) {}, "replacement_readiness", 1, 0},
		{"report predates the release", func(s *fakeGateStack, h *gateHooks) {
			h.candidateWorkers = 1
			s.observedAt = gateStart.Add(-time.Second)
		}, "replacement_readiness", 1, 0},
		{"report is stale", func(s *fakeGateStack, h *gateHooks) { h.candidateWorkers, s.stale = 1, true }, "replacement_readiness", 1, 0},
		{"observer never published", func(s *fakeGateStack, h *gateHooks) { h.candidateWorkers, s.unpublished = 1, true }, "replacement_readiness", 1, 0},
		{"report is truncated", func(s *fakeGateStack, h *gateHooks) { h.candidateWorkers, s.truncated = 1, true }, "replacement_readiness", 1, 0},
		{"workers outside the release serve the namespace", func(s *fakeGateStack, h *gateHooks) {
			h.candidateWorkers = 1
			s.workers["dep-elsewhere"] = 1
		}, "replacement_readiness", 1, 0},
		{"previous workers never leave", func(s *fakeGateStack, h *gateHooks) { h.candidateWorkers, h.keepPrevious = 1, true }, "drain_previous", 1, 1},
		{"migration check fails", func(s *fakeGateStack, h *gateHooks) { h.migrateErr = errors.New("password=secret") }, "migrations", 0, 0},
		{"start returns no workers", func(s *fakeGateStack, h *gateHooks) { h.startIDs = []string{} }, "start_replacements", 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stack := newGateStack()
			hooks := &gateHooks{}
			tc.prepare(stack, hooks)
			report := hooks.run(t, stack, filepath.Join(t.TempDir(), "journal.gate"))
			if report.OK || report.Stage != tc.stage || hooks.started != tc.started || hooks.drained != tc.drained {
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
// starting another generation, and keeps the original release start.
func TestMCPBuiltinGateResumesFromCheckpoint(t *testing.T) {
	stack := newGateStack()
	stack.set(func(s *fakeGateStack) {
		s.candidate = gateCandidateDeployment
		s.workers[gateCandidateDeployment] = 1
	})
	checkpoint := filepath.Join(t.TempDir(), "journal.gate")
	started := gateStart.Add(-time.Minute)
	body, _ := json.Marshal(map[string]any{"version": 1, "namespace": "reports", "releaseID": "release-1",
		"previousWorkerIDs": []string{}, "replacementWorkerIDs": []string{gateReplacement}, "startedAt": started.Format(time.RFC3339Nano)})
	if err := os.WriteFile(checkpoint, body, 0o600); err != nil {
		t.Fatal(err)
	}
	hooks := &gateHooks{}
	report := hooks.run(t, stack, checkpoint)
	if !report.OK || hooks.started != 0 || hooks.drained != 1 {
		t.Fatalf("report = %+v, hooks = %+v", report, hooks)
	}
}

func TestMCPReportReadinessCountsEveryReplacement(t *testing.T) {
	observed := gateStart.Add(time.Second)
	deployments := main.MCPGateDeployments{Candidate: gateCandidateDeployment, Previous: []string{gatePreviousDeployment}}
	report := func(handlers ...api.MCPTaskHandlerCompatibility) api.MCPTaskCompatibilityReportResponse {
		return api.MCPTaskCompatibilityReportResponse{Published: true, Handlers: handlers, ObservedAt: &observed}
	}
	handler := func(version string, deployments ...api.MCPTaskHandlerDeploymentWorkers) api.MCPTaskHandlerCompatibility {
		return api.MCPTaskHandlerCompatibility{ToolName: "build_report", HandlerVersion: version, WaitingTasks: 1, Deployments: deployments}
	}
	candidate := func(n int64) api.MCPTaskHandlerDeploymentWorkers {
		return api.MCPTaskHandlerDeploymentWorkers{DeploymentID: gateCandidateDeployment, Workers: n}
	}
	previous := api.MCPTaskHandlerDeploymentWorkers{DeploymentID: gatePreviousDeployment, Workers: 2}
	for _, tc := range []struct {
		name         string
		report       api.MCPTaskCompatibilityReportResponse
		replacements int
		ready, gone  bool
		err          error
	}{
		{"empty queue", report(), 1, true, true, nil},
		{"every version on every replacement", report(handler("v1", candidate(2), previous), handler("v2", candidate(2))), 2, true, false, nil},
		{"one replacement lacks a version", report(handler("v1", candidate(2)), handler("v2", candidate(1))), 2, false, true, nil},
		{"only previous workers serve a version", report(handler("v1", candidate(1)), handler("v0", previous)), 1, false, false, nil},
		{"unknown worker deployment", report(api.MCPTaskHandlerCompatibility{ToolName: "build_report", HandlerVersion: "v1", WaitingTasks: 1,
			UnknownDeploymentWorkers: 1, Deployments: []api.MCPTaskHandlerDeploymentWorkers{candidate(1)}}), 1, true, false, nil},
		{"outside deployment", report(handler("v1", candidate(1), api.MCPTaskHandlerDeploymentWorkers{DeploymentID: "dep-x", Workers: 1})), 1, false, true, main.ErrMCPGateOutsideWorkers},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ready, err := main.MCPReportReplacementsReady(tc.report, gateStart, deployments, tc.replacements)
			if ready != tc.ready || !errors.Is(err, tc.err) {
				t.Fatalf("ready = %v, %v", ready, err)
			}
			gone, err := main.MCPReportPreviousGone(tc.report, gateStart, deployments)
			if gone != tc.gone || err != nil {
				t.Fatalf("gone = %v, %v", gone, err)
			}
		})
	}
}

func TestRequireMCPWebTaskMigration(t *testing.T) {
	for _, tc := range []struct {
		runtime, manifest string
		ok                bool
	}{
		{"go", "release:\n  command: /app/out tasks-migrate apply\n", true},
		{"go", "release:\n  command: [\"/app/out\", \"tasks-migrate\", \"apply\"]\n", true},
		{"python", "release:\n  command: python task_schema.py apply\n", true},
		{"go", "hosting:\n  port: 8080\n", false},
		{"go", "release:\n  command: /app/out serve\n", false},
		{"python", "hosting:\n  command: python task_schema.py apply\n", false},
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), []byte(tc.manifest), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := main.RequireMCPWebTaskMigration(dir, tc.runtime); (err == nil) != tc.ok {
			t.Fatalf("%s %q: %v", tc.runtime, tc.manifest, err)
		}
	}
	if main.RequireMCPWebTaskMigration(t.TempDir(), "go") == nil {
		t.Fatal("missing gregale.yaml passed")
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
