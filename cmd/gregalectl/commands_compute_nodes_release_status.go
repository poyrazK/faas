package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

var releaseStatusSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

type computeNodesReleaseStatus struct {
	DesiredRelease  string                     `json:"desired_release"`
	ObservedAt      time.Time                  `json:"observed_at"`
	Ready           bool                       `json:"ready"`
	TimedOut        bool                       `json:"timed_out"`
	FailureReason   string                     `json:"failure_reason,omitempty"`
	ActiveNodeCount int                        `json:"active_node_count"`
	Nodes           []computeNodeReleaseStatus `json:"nodes"`
}

type computeNodeReleaseStatus struct {
	Name             string `json:"name"`
	DesiredRelease   string `json:"desired_release"`
	ObservedRelease  string `json:"observed_release,omitempty"`
	Lifecycle        string `json:"lifecycle"`
	LastHeartbeatAt  string `json:"last_heartbeat_at,omitempty"`
	HeartbeatAgeMS   int64  `json:"heartbeat_age_ms,omitempty"`
	Ready            bool   `json:"ready"`
	ReadinessFailure string `json:"readiness_failure,omitempty"`
}

func cmdComputeNodesReleaseStatus(args []string) int {
	fs := flag.NewFlagSet("release-status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	desired := fs.String("desired-release", "", "40-character release git SHA expected on every active node")
	timeout := fs.Duration("timeout", 0, "maximum time to wait for fleet convergence (zero performs one observation)")
	pollInterval := fs.Duration("poll-interval", 10*time.Second, "interval between convergence observations")
	heartbeatStaleness := fs.Duration("heartbeat-staleness", state.DefaultHeartbeatStaleness, "maximum age of a ready node heartbeat")
	jsonOut := fs.Bool("json", false, "emit structured JSON to stdout")
	breakGlass := fs.Bool("break-glass-db", false, "read directly from the database during an apid outage")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "gregalectl compute-nodes release-status: unexpected positional args")
		return 2
	}
	if !releaseStatusSHA.MatchString(*desired) {
		fmt.Fprintln(os.Stderr, "gregalectl compute-nodes release-status: --desired-release must be a 40-character lowercase git SHA")
		return 2
	}
	if *timeout < 0 || *pollInterval <= 0 || *heartbeatStaleness <= 0 {
		fmt.Fprintln(os.Stderr, "gregalectl compute-nodes release-status: timeout must be non-negative; poll-interval and heartbeat-staleness must be positive")
		return 2
	}

	ctx := context.Background()
	started := time.Now()
	deadline := started.Add(*timeout)
	for {
		now := time.Now().UTC()
		var nodes []state.ComputeNode
		var err error
		if *breakGlass {
			nodes, err = computeNodesListFromDB(true)
		} else {
			nodes, err = computeNodesListFromAPI(true)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "gregalectl compute-nodes release-status: observe active nodes: %v\n", err)
			return 1
		}
		report := evaluateComputeNodesReleaseStatus(nodes, *desired, now, *heartbeatStaleness)
		if report.Ready {
			emitComputeNodesReleaseStatus(osStdout, report, *jsonOut)
			return 0
		}
		if *timeout == 0 || !time.Now().Before(deadline) {
			report.TimedOut = *timeout > 0
			emitComputeNodesReleaseStatus(osStdout, report, *jsonOut)
			return 3
		}

		wait := *pollInterval
		if remaining := time.Until(deadline); remaining < wait {
			wait = remaining
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			fmt.Fprintf(os.Stderr, "gregalectl compute-nodes release-status: %v\n", ctx.Err())
			return 1
		case <-timer.C:
		}
	}
}

func evaluateComputeNodesReleaseStatus(nodes []state.ComputeNode, desired string, now time.Time, heartbeatStaleness time.Duration) computeNodesReleaseStatus {
	report := computeNodesReleaseStatus{
		DesiredRelease: desired, ObservedAt: now.UTC(), ActiveNodeCount: len(nodes),
		Nodes: make([]computeNodeReleaseStatus, 0, len(nodes)),
	}
	if len(nodes) == 0 {
		report.FailureReason = "no_active_nodes"
		return report
	}
	report.Ready = true
	for _, node := range nodes {
		observed := ""
		if node.ReleaseID != nil {
			observed = *node.ReleaseID
		}
		status := computeNodeReleaseStatus{
			Name: node.Name, DesiredRelease: desired, ObservedRelease: observed,
			Lifecycle: string(effectiveComputeNodeLifecycle(node)), LastHeartbeatAt: formatComputeNodeTime(node.LastHeartbeatAt),
		}
		if !node.LastHeartbeatAt.IsZero() {
			age := now.Sub(node.LastHeartbeatAt)
			if age < 0 {
				age = 0
			}
			status.HeartbeatAgeMS = age.Milliseconds()
		}
		switch {
		case !node.Active || effectiveComputeNodeLifecycle(node) != state.NodeLifecycleActive:
			status.ReadinessFailure = "lifecycle_not_active"
		case observed == "":
			status.ReadinessFailure = "observed_release_missing"
		case observed != desired:
			status.ReadinessFailure = "release_mismatch"
		case node.LastHeartbeatAt.IsZero():
			status.ReadinessFailure = "heartbeat_missing"
		case now.Sub(node.LastHeartbeatAt) > heartbeatStaleness:
			status.ReadinessFailure = "heartbeat_stale"
		default:
			status.Ready = true
		}
		if !status.Ready {
			report.Ready = false
		}
		report.Nodes = append(report.Nodes, status)
	}
	if !report.Ready {
		report.FailureReason = "active_nodes_not_converged"
	}
	return report
}

func emitComputeNodesReleaseStatus(w io.Writer, report computeNodesReleaseStatus, jsonOut bool) {
	if jsonOut {
		_ = json.NewEncoder(w).Encode(report)
		return
	}
	stateText := "ready"
	if !report.Ready {
		stateText = "not-ready"
	}
	_, _ = fmt.Fprintf(w, "fleet release=%s state=%s active_nodes=%d timed_out=%t\n", report.DesiredRelease, stateText, report.ActiveNodeCount, report.TimedOut)
	for _, node := range report.Nodes {
		observed := node.ObservedRelease
		if observed == "" {
			observed = "(missing)"
		}
		reason := node.ReadinessFailure
		if reason == "" {
			reason = "ready"
		}
		_, _ = fmt.Fprintf(w, "  node=%s desired=%s observed=%s lifecycle=%s ready=%t reason=%s\n", node.Name, node.DesiredRelease, observed, node.Lifecycle, node.Ready, reason)
	}
}
