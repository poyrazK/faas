package main

import (
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

func TestCmdObsDeploymentQualificationEvidencePrivateRecovery(t *testing.T) {
	stdout, stderr := obsBundleTestStreams(t)
	start := time.Now().UTC()
	after := start.Add(-30 * time.Second)
	oldRun := start.Add(-time.Minute)
	var requests, servingReads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer private-qualification-token" {
			t.Error("qualification diagnostics changed the protected read contract")
		}
		snapshot := deploymentSnapshotTest(time.Now().UTC())
		switch r.URL.Path {
		case operatorobs.ServingHealthPath:
			if servingReads.Add(1) == 1 {
				snapshot.serving.value.Probes[0].LastRunAt = &oldRun
			} else {
				run := time.Now().UTC().Add(-time.Second)
				snapshot.serving.value.Probes[0].LastRunAt = &run
			}
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
	args := []string{subObsVerifyDeployment, "--release", strings.Repeat("a", 40), "--after", after.Format(time.RFC3339Nano), "--admin-token", "private-qualification-token", "--wait-timeout", "10s", "--poll-interval", "1s", "--report", path, "--json"}
	if code := cmdObsDispatch(args); code != 0 {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report obsDeploymentReport
	if err := json.Unmarshal(body, &report); err != nil || report.State != "passed" || report.HadConfirmedFaults || report.Attempts != 2 || requests.Load() != 4 || report.QualificationEvidence == nil {
		t.Fatalf("qualification diagnostics changed convergence or added reads: %s %v", body, err)
	}
	check := report.QualificationEvidence.Checks[0]
	if check.State != "passed" || check.Blockers != nil || check.LastBlocked == nil || check.LastBlocked.Blockers.Total != 1 {
		t.Fatal("passing report lost the preceding readable blocker")
	}
	item := check.LastBlocked.Blockers.Items[0]
	if item.Kind != "probe_run" || item.Node != "fsn-2" || item.Reason != "before_deployment_boundary" || !item.ObservedAt.Equal(oldRun) || !item.ObservedAt.Before(after) || item.MaxAgeSeconds != 900 {
		t.Fatal("probe blocker lost its identity, original clock or age policy")
	}
	progress := check.Progress
	history := report.QualificationEvidence.History
	if progress == nil || progress.FirstBlockedAt == nil || progress.FirstPassedAt == nil || progress.ObservedBlockedSeconds <= 0 || progress.ObservedUnavailableSeconds != 0 || progress.BlockersSince != nil || history == nil || history.TotalEvents != 4 || history.Events[0].State != "blocked" || history.Events[3].State != "passed" || !history.Events[0].Blockers.Items[0].ObservedAt.Equal(oldRun) {
		t.Fatal("private passing report lost blocker-change history or waiting time")
	}
	if report.Sources.ServingHealth.FailedReads != 0 || report.Sources.ServingHealth.Reads != 2 || report.Sources.NotificationHealth.FailedReads != 0 {
		t.Fatal("unusable evidence was reclassified as a failed read")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 || strings.Contains(string(body)+stdout.String()+stderr.String(), "private-") {
		t.Fatal("report redaction or private permissions failed")
	}
}
