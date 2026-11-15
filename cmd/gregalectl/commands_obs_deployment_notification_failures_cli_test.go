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

func TestCmdObsDeploymentNotificationFailuresPrivateRecoveryReport(t *testing.T) {
	stdout, stderr := obsBundleTestStreams(t)
	var requests, notificationReads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer private-notification-fault-token" {
			t.Error("fault retention changed the protected read contract")
		}
		snapshot := deploymentSnapshotTest(time.Now().UTC())
		switch r.URL.Path {
		case operatorobs.ServingHealthPath:
			_ = json.NewEncoder(w).Encode(snapshot.serving.value)
		case operatorobs.NotificationHealthPath:
			if notificationReads.Add(1) == 1 {
				snapshot = deploymentNotificationFaultFixture(time.Now().UTC())
				*snapshot.notifications.Signals["alertmanager_failures"].Value = 2
			}
			_ = json.NewEncoder(w).Encode(snapshot.notifications)
		default:
			t.Errorf("unexpected endpoint: %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_APID_URL", srv.URL)
	path := filepath.Join(t.TempDir(), "report.json")
	args := []string{subObsVerifyDeployment, "--release", strings.Repeat("a", 40), "--after", time.Now().Add(-90 * time.Second).UTC().Format(time.RFC3339), "--admin-token", "private-notification-fault-token", "--wait-timeout", "10s", "--poll-interval", "1s", "--report", path, "--json"}
	if code := cmdObsDispatch(args); code != 0 {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report obsDeploymentReport
	if err := json.Unmarshal(body, &report); err != nil || report.State != "passed" || !report.HadConfirmedFaults || report.Attempts != 2 || requests.Load() != 4 || report.Timeline == nil || report.Timeline.NotificationEvidence.RetainedItems != 2 {
		t.Fatalf("recovery report lost original faults or added reads: %s %v", body, err)
	}
	first := report.Timeline.Transitions[2].NotificationFailures
	if first == nil || first.Items[0].Receiver != "faas-page" || first.Items[0].Integration != "slack" || *first.Items[0].Signals[0].Value != 1.5 || first.Items[0].Signals[0].WindowSeconds != 600 {
		t.Fatal("recovery report omitted the original receiver failure")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 || strings.Contains(string(body)+stdout.String()+stderr.String(), "private-notification-fault-token") {
		t.Fatal("report permissions or credential redaction failed")
	}
}
