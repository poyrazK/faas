package tasktoolrelease_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func TestMCPTaskRecoveryReadOnlyEvidence(t *testing.T) {
	for _, scenario := range []string{"promoted", "lost_worker_reply", "ambiguous_history", "missing_history", "cursor_cycle", "missing_checkpoint", "lost_promotion_reply", "foreign_traffic", "previous_generation_changed", "stale_observer", "journal_changed", "app_unavailable", "lost_park_reply", "baseline_restored"} {
		t.Run(scenario, func(t *testing.T) {
			record, now := runningReceiptFixture(t)
			record.Execution.State = "reconcile_required"
			webID, workerID := uuid.NewString(), uuid.NewString()
			checkpoint := api.TaskToolReleaseReceipt{Revision: 7, ReleaseID: tasktoolrelease.ExecutionReleaseID(record), Stage: "web_promoted", ServingCaptured: true,
				WebDeploymentID: webID, WorkerDeploymentID: workerID, WorkerIDs: []string{uuid.NewString()}, Promoted: true, PreviousDeployments: map[string]string{}, Parked: map[string]bool{}}
			reviews := map[string]api.AppReleaseReview{}
			instances := map[string][]api.InstanceResponse{}
			deployments := map[string]api.DeploymentResponse{}
			for _, pin := range record.Intent.Apps {
				review := pin.Review
				current := pin.Review.LatestDeploymentID
				switch pin.Role {
				case "web":
					checkpoint.ServingDeploymentID = current
					deployments[current] = api.DeploymentResponse{ID: current, AppID: pin.Review.AppID, Status: "live", Scope: api.DefaultEnvScope}
					current = webID
					review.ServingDeploymentIDs = []string{webID}
				case "worker":
					current = workerID
				case "previous_worker":
					checkpoint.PreviousDeployments[pin.App] = current
				}
				review.LatestDeploymentID = current
				reviews[pin.App] = review
				instances[pin.App] = []api.InstanceResponse{{ID: uuid.NewString(), AppID: pin.Review.AppID, DeploymentID: current, State: "running"}}
				if pin.Role == "web" || pin.Role == "worker" {
					deployments[current] = api.DeploymentResponse{ID: current, AppID: pin.Review.AppID, Status: "live", Scope: api.DefaultEnvScope, Reason: tasktoolrelease.SubmissionReason(checkpoint.ReleaseID, pin.Role)}
				}
			}
			pending := scenario == "lost_worker_reply" || scenario == "ambiguous_history" || scenario == "missing_history" || scenario == "cursor_cycle"
			if pending {
				checkpoint.PendingSubmission = "worker"
				checkpoint.WorkerDeploymentID = ""
				checkpoint.WebDeploymentID = ""
				checkpoint.WorkerIDs = []string{}
				checkpoint.Promoted = false
				checkpoint.Stage = "deploying_worker"
				review := reviews["web"]
				review.LatestDeploymentID = checkpoint.ServingDeploymentID
				review.ServingDeploymentIDs = []string{checkpoint.ServingDeploymentID}
				reviews["web"] = review
			}
			switch scenario {
			case "lost_promotion_reply":
				checkpoint.Promoted = false
				checkpoint.Stage = "replacements_started"
			case "foreign_traffic":
				review := reviews["web"]
				review.ServingDeploymentIDs = []string{uuid.NewString()}
				reviews["web"] = review
			case "previous_generation_changed":
				review := reviews["previous"]
				review.LatestDeploymentID = uuid.NewString()
				reviews["previous"] = review
			case "lost_park_reply":
				instances["previous"] = []api.InstanceResponse{}
			case "baseline_restored":
				review := reviews["web"]
				review.ServingDeploymentIDs = []string{checkpoint.ServingDeploymentID}
				reviews["web"] = review
			}
			record.Execution.Receipt = &checkpoint
			record.Execution.CompletedStages = tasktoolrelease.ReceiptStages(checkpoint)
			if scenario == "missing_checkpoint" {
				record.Execution.Receipt = nil
				record.Execution.CompletedStages = nil
			}
			var writes, historyPages, reads atomic.Int32
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					writes.Add(1)
					t.Error("recovery attempted a mutation", r.Method, r.URL.Path)
					w.WriteHeader(500)
					return
				}
				var out any
				switch {
				case strings.HasPrefix(r.URL.Path, "/v1/task-tool-release-intents/"):
					count := reads.Add(1)
					copyRecord := record
					if scenario == "journal_changed" && count > 1 {
						copyExec := *record.Execution
						copyExec.State = "running"
						copyRecord.Execution = &copyExec
					}
					out = copyRecord
				case strings.HasPrefix(r.URL.Path, "/v1/deployments/"):
					id := strings.TrimPrefix(r.URL.Path, "/v1/deployments/")
					dep, ok := deployments[id]
					if !ok {
						w.WriteHeader(404)
						return
					}
					out = dep
				case strings.HasSuffix(r.URL.Path, "/release-review"):
					slug := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/apps/"), "/release-review")
					if scenario == "app_unavailable" && slug == "worker" {
						w.WriteHeader(503)
						_, _ = w.Write([]byte(`{"detail":"SECRET_SENTINEL"}`))
						return
					}
					out = reviews[slug]
				case strings.HasSuffix(r.URL.Path, "/instances"):
					slug := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/apps/"), "/instances")
					out = instances[slug]
				case strings.HasSuffix(r.URL.Path, "/custom-metrics"):
					out = api.CustomMetricListResponse{FreshnessS: api.CustomMetricFreshnessSeconds, Metrics: []api.CustomMetricResponse{{Name: "mcp_tasks_observer_heartbeat", Value: 1, ObservedAt: now.Add(-time.Second), Stale: scenario == "stale_observer"}}}
				case strings.HasSuffix(r.URL.Path, "/deployments"):
					historyPages.Add(1)
					if r.URL.Query().Get("limit") != "200" {
						t.Error("history page is unbounded")
					}
					if r.URL.Query().Get("before") == "" {
						out = api.DeploymentListResponse{Items: []api.DeploymentResponse{}, NextBefore: "page-two"}
					} else {
						items := []api.DeploymentResponse{deployments[workerID]}
						if scenario == "missing_history" {
							items = []api.DeploymentResponse{}
						}
						if scenario == "ambiguous_history" {
							other := deployments[workerID]
							other.ID = uuid.NewString()
							items = append(items, other)
						}
						next := ""
						if scenario == "cursor_cycle" {
							next = "page-two"
						}
						out = api.DeploymentListResponse{Items: items, NextBefore: next}
					}
				default:
					t.Error("unexpected native GET", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				_ = json.NewEncoder(w).Encode(out)
			}))
			defer backend.Close()
			report, err := tasktoolrelease.InspectRecovery(t.Context(), api.NewClient(backend.URL, "fixture-token"), record.ID, true, now)
			if err != nil {
				t.Fatal(err)
			}
			if writes.Load() != 0 || !report.ReadOnly || report.ReadyToApply || !report.OwnerAuthorizationRequired || !report.ExpiresAt.Equal(now.Add(api.TaskToolRecoveryObservationTTL)) || !toolpublication.ValidID(report.EvidenceSHA256) || !toolpublication.ValidID(report.RecoveryPlanID) {
				t.Fatal("invalid read-only evidence", report)
			}
			b, _ := json.Marshal(report)
			if strings.Contains(string(b), "SECRET_SENTINEL") || strings.Contains(string(b), "fixture-token") {
				t.Fatal("native error or credential leaked")
			}
			actions := map[string]string{}
			for _, action := range report.Actions {
				actions[action.Action] = action.Eligibility
				if len(action.RequiredOwnerChecks) == 0 {
					t.Fatal("plan omitted fresh owner checks")
				}
			}
			switch scenario {
			case "promoted", "lost_promotion_reply":
				if actions["resume"] != "requires_owner_checks" || actions["restore"] != "requires_owner_checks" || actions["retire"] != "blocked" {
					t.Fatal(actions, report.Checks)
				}
			case "lost_worker_reply":
				if historyPages.Load() != 2 || actions["resume"] != "requires_owner_checks" {
					t.Fatal("lost reply was not uniquely reconciled through all pages", actions, report.Checks)
				}
				if report.Receipt.PendingSubmission != "worker" || report.Receipt.WorkerDeploymentID != "" {
					t.Fatal("read-only inspection rewrote checkpoint")
				}
			case "lost_park_reply":
				if actions["resume"] != "requires_owner_checks" || actions["restore"] != "blocked" {
					t.Fatal(actions, report.Checks)
				}
			case "baseline_restored":
				if actions["retire"] != "requires_owner_checks" || actions["resume"] != "blocked" {
					t.Fatal(actions, report.Checks)
				}
			default:
				for _, eligibility := range actions {
					if eligibility != "blocked" {
						t.Fatal("uncertain evidence produced an eligible action", actions, report.Checks)
					}
				}
			}
			if (scenario == "ambiguous_history" || scenario == "missing_history" || scenario == "cursor_cycle") && historyPages.Load() != 2 {
				t.Fatal("history was not safely paginated", historyPages.Load())
			}
		})
	}
}
