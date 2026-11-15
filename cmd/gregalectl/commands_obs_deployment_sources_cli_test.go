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

func TestCmdObsDeploymentSourceReadsPrivateRecoveryReport(t *testing.T) {
	stdout, stderr := obsBundleTestStreams(t)
	var requests, notificationReads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer private-source-read-token" {
			t.Error("source diagnostics changed the protected read contract")
		}
		snapshot := deploymentSnapshotTest(time.Now().UTC())
		switch r.URL.Path {
		case operatorobs.ServingHealthPath:
			_ = json.NewEncoder(w).Encode(snapshot.serving.value)
		case operatorobs.NotificationHealthPath:
			count := notificationReads.Add(1)
			if count <= 3 {
				status := 403
				if count == 2 {
					status = 503
				}
				if count == 3 {
					status = 502
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte("private-provider-response"))
				return
			}
			_ = json.NewEncoder(w).Encode(snapshot.notifications)
		default:
			t.Errorf("unexpected endpoint: %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_APID_URL", srv.URL)
	path := filepath.Join(t.TempDir(), "report.json")
	args := []string{subObsVerifyDeployment, "--release", strings.Repeat("a", 40), "--after", time.Now().Add(-90 * time.Second).UTC().Format(time.RFC3339), "--admin-token", "private-source-read-token", "--wait-timeout", "10s", "--poll-interval", "1s", "--report", path, "--json"}
	if code := cmdObsDispatch(args); code != 0 {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report obsDeploymentReport
	if err := json.Unmarshal(body, &report); err != nil || report.State != "passed" || report.HadConfirmedFaults || report.Attempts != 4 || requests.Load() != 8 || report.Sources == nil {
		t.Fatalf("read diagnostics changed qualification or added reads: %s %v", body, err)
	}
	source := report.Sources.NotificationHealth
	if source.Reads != 4 || source.FailedReads != 3 || source.CancelledReads != 0 || source.State != "succeeded" || *source.HTTPStatus != 200 || *source.LastFailure.HTTPStatus != 502 || source.LastRecoveryAt == nil || source.LastSuccessAt == nil || source.LastReadSeconds == nil || *source.LastReadSeconds < 0 || source.LastReadStartedAt == nil || !source.LastReadCompletedAt.Equal(*source.LastSuccessAt) {
		t.Fatalf("private report lost source history or timing: %+v", source)
	}
	var statuses []int
	for _, transition := range report.Timeline.Transitions {
		if transition.Check == "notification_pipeline" && transition.SourceHTTPStatus != nil {
			statuses = append(statuses, *transition.SourceHTTPStatus)
		}
	}
	if len(statuses) != 3 || statuses[0] != 403 || statuses[1] != 503 || statuses[2] != 502 {
		t.Fatalf("HTTP status transitions were lost: %v", statuses)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 || strings.Contains(string(body)+stdout.String()+stderr.String(), "private-") {
		t.Fatal("source diagnostics leaked credentials/provider text or lost private permissions")
	}
}
