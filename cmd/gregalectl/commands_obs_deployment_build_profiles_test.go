package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func TestCmdDeploymentV2ProfilesKeepBuildFailuresAdvisory(t *testing.T) {
	for _, role := range []string{"single-box-v2", "control-plane-v2"} {
		for _, olderAPI := range []bool{false, true} {
			t.Run(role+map[bool]string{true: "/older-api", false: "/mixed-builder"}[olderAPI], func(t *testing.T) {
				stdout, stderr := obsBundleTestStreams(t)
				now := time.Now().UTC()
				after := now.Add(-90 * time.Second).Truncate(time.Second)
				release := strings.Repeat("a", 40)
				builds := deploymentRuntimeBuildProfileTest(t, now, after, release, role)
				builds.State, builds.Reason = "mixed", "multiple_observed_revisions"
				builds.Counts.Matching--
				builds.Counts.Mismatched, builds.Counts.DistinctRevisions = 1, 2
				for i := range builds.Items {
					if builds.Items[i].Job == "builderd" {
						builds.Items[i].State, builds.Items[i].Reason, builds.Items[i].GitSHA = "mismatched", "requested_revision_mismatch", strings.Repeat("b", 40)
					}
				}
				var calls atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer profile-private-token" {
						t.Error("unprotected profile read")
					}
					snapshot := deploymentSnapshotTest(time.Now().UTC())
					switch r.URL.Path {
					case operatorobs.RuntimeBuildsPath:
						if len(r.URL.Query()) != 3 || r.URL.Query().Get("expected_role") != role {
							t.Error("profile selection changed")
						}
						if olderAPI {
							w.WriteHeader(400)
							_, _ = w.Write([]byte("private-provider-error"))
							return
						}
						_ = json.NewEncoder(w).Encode(builds)
					case operatorobs.ServingHealthPath:
						_ = json.NewEncoder(w).Encode(snapshot.serving.value)
					case operatorobs.NotificationHealthPath:
						_ = json.NewEncoder(w).Encode(snapshot.notifications)
					default:
						t.Error("unexpected read", r.URL.Path)
					}
				}))
				defer srv.Close()
				t.Setenv("FAAS_APID_URL", srv.URL)
				code := cmdObsVerifyDeployment([]string{"--release", release, "--after", after.Format(time.RFC3339), "--expected-role", role, "--admin-token", "profile-private-token", "--json"})
				var report obsDeploymentReport
				if code != 0 || calls.Load() != 3 || json.Unmarshal(stdout.Bytes(), &report) != nil || report.State != "passed" || report.HadConfirmedFaults || report.RuntimeBuilds.Progress.Observations != 1 {
					t.Fatalf("profile changed health qualification: code=%d stdout=%s stderr=%s", code, stdout, stderr)
				}
				if olderAPI {
					if report.RuntimeBuilds.State != "unknown" || *report.RuntimeBuilds.SourceHTTPStatus != 400 || !report.RuntimeBuilds.Progress.ObservedSourceUnavailable {
						t.Fatal("older API did not remain unknown")
					}
				} else {
					if report.RuntimeBuilds.State != "mixed" || len(report.RuntimeBuilds.Evidence.Jobs) != len(builds.Jobs) || report.RuntimeBuilds.History.Events[0].Counts.Mismatched != 1 || report.RuntimeBuilds.Progress.FirstMismatchAt == nil {
						t.Fatal("v2 job/history evidence lost")
					}
				}
				if strings.Contains(stdout.String(), "profile-private-token") || strings.Contains(stdout.String(), "private-provider-error") {
					t.Fatal("provider/credential leaked")
				}
			})
		}
	}
}
