package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func TestCmdObsVerifyDeploymentStability(t *testing.T) {
	stdout, stderr := obsBundleTestStreams(t)
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer private-stability-token" {
			t.Error("stability verification changed the protected read contract")
		}
		snapshot := deploymentSnapshotTest(time.Now().UTC())
		switch r.URL.Path {
		case operatorobs.ServingHealthPath:
			_ = json.NewEncoder(w).Encode(snapshot.serving.value)
		case operatorobs.NotificationHealthPath:
			_ = json.NewEncoder(w).Encode(snapshot.notifications)
		default:
			t.Errorf("unexpected endpoint: %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_APID_URL", srv.URL)
	path := filepath.Join(t.TempDir(), "report.json")
	args := []string{subObsVerifyDeployment, "--release", strings.Repeat("a", 40), "--after", time.Now().Add(-90 * time.Second).UTC().Format(time.RFC3339), "--admin-token", "private-stability-token", "--stable-for", "1s", "--wait-timeout", "10s", "--poll-interval", "1s", "--report", path, "--json"}
	if code := cmdObsDispatch(args); code != 0 {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report obsDeploymentReport
	if err := json.Unmarshal(body, &report); err != nil || report.Stability == nil || report.Stability.State != "passed" || report.Stability.ObservedSeconds < 1 || report.Attempts < 2 || report.Attempts > 3 || int(requests.Load()) != 2*report.Attempts {
		t.Fatalf("invalid stability report: %s, %v", body, err)
	}
	if d := report.Stability.Diagnostics; d == nil || d.State != "available" || d.ObservationRemainingSeconds != 0 || d.Blockers == nil || d.Blockers.Total != 0 || d.Blockers.Truncated {
		t.Fatal("passing private report lost source diagnostics")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 || strings.Contains(string(body)+stdout.String()+stderr.String(), "private-stability-token") {
		t.Fatal("stability report permissions or redaction failed")
	}
}

func TestObsDeploymentStabilityOptionsAndDefault(t *testing.T) {
	_, _ = obsBundleTestStreams(t)
	base := []string{"--release", strings.Repeat("a", 40), "--after", time.Now().Add(-90 * time.Second).UTC().Format(time.RFC3339), "--admin-token", "fixture"}
	for _, extra := range [][]string{{"--stable-for", "-1s"}, {"--stable-for", "61m"}, {"--stable-for", "2m", "--wait-timeout", "1m"}, {"--stable-for", "invalid"}} {
		if _, _, ok := parseObsDeploymentOptions(append(append([]string{}, base...), extra...)); ok {
			t.Fatalf("invalid stability option accepted: %v", extra)
		}
	}
	for _, extra := range [][]string{nil, {"--stable-for", "0s"}, {"--stable-for", "2m", "--wait-timeout", "5m"}, {"--stable-for", "1h", "--wait-timeout", "1h"}, {"--stable-for", "2m"}} {
		opts, _, ok := parseObsDeploymentOptions(append(append([]string{}, base...), extra...))
		if !ok {
			t.Fatalf("valid stability option rejected: %v", extra)
		}
		if opts.stableFor != 0 {
			continue
		}
		now := time.Now().UTC()
		calls, writes := 0, 0
		report, code := runObsDeploymentVerification(context.Background(), opts, func(context.Context) obsDeploymentSnapshot {
			calls++
			return deploymentSnapshotTest(now)
		}, func() time.Time { return now }, func(context.Context, time.Duration) error { t.Fatal("default added a wait"); return nil }, func(obsDeploymentReport) error { writes++; return nil })
		body, err := json.Marshal(report)
		if code != 0 || calls != 1 || writes != 2 || report.Stability != nil || err != nil || strings.Contains(string(body), "stability") {
			t.Fatal("disabled stability changed the first-pass contract")
		}
	}
}

func TestCmdObsVerifyDeploymentStabilityTimeoutDiagnostics(t *testing.T) {
	stdout, stderr := obsBundleTestStreams(t)
	start := time.Now().UTC()
	run := start.Add(-time.Minute)
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer private-diagnostics-token" {
			t.Error("diagnostic verification changed the protected read contract")
		}
		snapshot := deploymentSnapshotTest(time.Now().UTC())
		snapshot.serving.value.Probes[0].LastRunAt = &run
		switch r.URL.Path {
		case operatorobs.ServingHealthPath:
			_ = json.NewEncoder(w).Encode(snapshot.serving.value)
		case operatorobs.NotificationHealthPath:
			_ = json.NewEncoder(w).Encode(snapshot.notifications)
		default:
			t.Errorf("unexpected endpoint: %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_APID_URL", srv.URL)
	path := filepath.Join(t.TempDir(), "report.json")
	args := []string{subObsVerifyDeployment, "--release", strings.Repeat("a", 40), "--after", start.Add(-90 * time.Second).Format(time.RFC3339Nano), "--admin-token", "private-diagnostics-token", "--stable-for", "1s", "--wait-timeout", "2100ms", "--poll-interval", "1s", "--report", path, "--json"}
	if code := cmdObsDispatch(args); code != 3 {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report obsDeploymentReport
	if err := json.Unmarshal(body, &report); err != nil || report.Stability == nil || report.Stability.Diagnostics == nil || requests.Load() < 4 {
		t.Fatalf("missing timeout diagnostics: %s, %v", body, err)
	}
	d := report.Stability.Diagnostics
	preview := d.Blockers
	if d.State == "unavailable" && d.LastReset != nil && d.LastReset.SourceContext == "previous_window_blockers" {
		// An expired final read preserves the preceding window as history.
		preview = d.LastReset.Sources
	}
	if preview == nil || preview.Total != 1 || len(preview.Items) != 1 || preview.Items[0].Kind != "probe_run" || *preview.Items[0].AdvancedSeconds != 0 || *preview.Items[0].RemainingSeconds != 1 || !preview.Items[0].LatestAt.Equal(run) {
		t.Fatalf("timeout hid or re-stamped the frozen probe: %+v", d)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 || strings.Contains(string(body)+stdout.String()+stderr.String(), "private-diagnostics-token") {
		t.Fatal("timeout report permissions or redaction failed")
	}
}
