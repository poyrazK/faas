package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEvaluateComputeNodesReleaseStatusTracksDesiredAndObserved(t *testing.T) {
	now := time.Date(2026, 9, 12, 18, 0, 0, 0, time.UTC)
	desired := strings.Repeat("a", 40)
	old := strings.Repeat("b", 40)
	report := evaluateComputeNodesReleaseStatus([]state.ComputeNode{
		{Name: "ssd", Active: true, Lifecycle: state.NodeLifecycleActive, ReleaseID: &desired, LastHeartbeatAt: now.Add(-30 * time.Second)},
		{Name: "hdd", Active: true, Lifecycle: state.NodeLifecycleActive, ReleaseID: &old, LastHeartbeatAt: now.Add(-5 * time.Second)},
	}, desired, now, state.DefaultHeartbeatStaleness)

	if report.Ready || report.FailureReason != "active_nodes_not_converged" {
		t.Fatalf("report = %+v, want a visible incomplete rollout", report)
	}
	if got := report.Nodes[0]; !got.Ready || got.DesiredRelease != desired || got.ObservedRelease != desired {
		t.Fatalf("matching node = %+v", got)
	}
	if got := report.Nodes[1]; got.Ready || got.DesiredRelease != desired || got.ObservedRelease != old || got.ReadinessFailure != "release_mismatch" {
		t.Fatalf("skewed node = %+v", got)
	}
}

func TestEvaluateComputeNodesReleaseStatusRequiresFreshHeartbeat(t *testing.T) {
	now := time.Date(2026, 9, 12, 18, 0, 0, 0, time.UTC)
	desired := strings.Repeat("c", 40)
	tests := []struct {
		name      string
		heartbeat time.Time
		want      string
	}{
		{name: "missing", want: "heartbeat_missing"},
		{name: "stale", heartbeat: now.Add(-state.DefaultHeartbeatStaleness - time.Second), want: "heartbeat_stale"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report := evaluateComputeNodesReleaseStatus([]state.ComputeNode{{
				Name: "compute-1", Active: true, Lifecycle: state.NodeLifecycleActive,
				ReleaseID: &desired, LastHeartbeatAt: tc.heartbeat,
			}}, desired, now, state.DefaultHeartbeatStaleness)
			if report.Ready || report.Nodes[0].ReadinessFailure != tc.want {
				t.Fatalf("report = %+v, want %s", report, tc.want)
			}
		})
	}
}

func TestEvaluateComputeNodesReleaseStatusRejectsEmptyActiveFleet(t *testing.T) {
	report := evaluateComputeNodesReleaseStatus(nil, strings.Repeat("d", 40), time.Now(), state.DefaultHeartbeatStaleness)
	if report.Ready || report.FailureReason != "no_active_nodes" || report.ActiveNodeCount != 0 {
		t.Fatalf("report = %+v", report)
	}
}

func TestEmitComputeNodesReleaseStatusJSONIsMachineReadable(t *testing.T) {
	desired := strings.Repeat("e", 40)
	report := computeNodesReleaseStatus{
		DesiredRelease: desired, ObservedAt: time.Date(2026, 9, 12, 18, 0, 0, 0, time.UTC),
		Ready: false, TimedOut: true, FailureReason: "active_nodes_not_converged", ActiveNodeCount: 1,
		Nodes: []computeNodeReleaseStatus{{Name: "compute-1", DesiredRelease: desired, ObservedRelease: strings.Repeat("f", 40), ReadinessFailure: "release_mismatch"}},
	}
	var out bytes.Buffer
	emitComputeNodesReleaseStatus(&out, report, true)
	var decoded computeNodesReleaseStatus
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("unmarshal release status: %v (raw %q)", err, out.String())
	}
	if !decoded.TimedOut || decoded.Nodes[0].DesiredRelease != desired || decoded.Nodes[0].ReadinessFailure != "release_mismatch" {
		t.Fatalf("decoded = %+v", decoded)
	}
}

func TestCmdComputeNodesReleaseStatusRejectsInvalidRelease(t *testing.T) {
	if code := cmdComputeNodesReleaseStatus([]string{"--desired-release=latest"}); code != 2 {
		t.Fatalf("exit = %d, want usage exit 2", code)
	}
}

func TestComputeNodeFromOperatorResponseParsesHeartbeat(t *testing.T) {
	stamp := "2026-09-12T18:00:00.123456Z"
	row := computeNodeFromOperatorResponse(api.ComputeNodeOperatorResponse{LastHeartbeatAt: stamp})
	if got := formatComputeNodeTime(row.LastHeartbeatAt); got != "2026-09-12T18:00:00.123456Z" {
		t.Fatalf("heartbeat = %q", got)
	}
}
