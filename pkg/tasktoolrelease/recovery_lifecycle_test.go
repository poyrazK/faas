package tasktoolrelease_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func recoveryLifecycleFixture(t *testing.T, stage string) (api.TaskToolReleaseIntentRecord, time.Time) {
	t.Helper()
	var r api.TaskToolReleaseIntentRecord
	var err error
	var now time.Time
	switch stage {
	case "handover_pending":
		var in api.TaskToolReleaseIntentDecision
		r, in, now = handoverFixture(t)
		r, err = tasktoolrelease.Recovery(r, in, true, now)
	case "executor_exited", "executor_fenced", "restore_qualified", "restore_approved":
		r, _, now = restoreFixture(t)
		if stage != "restore_approved" {
			h := r.Execution.Recovery
			h.Decision, h.AuthorizerKeyID, h.DecidedAt = "", "", nil
		}
		if stage == "executor_fenced" {
			r.Execution.Recovery.Qualification = nil
		}
		if stage == "executor_exited" {
			r.Execution.Recovery = nil
		}
	case "restored", "quarantine_qualified", "quarantine_approved", "quarantine_applying", "quarantined":
		var in api.TaskToolReleaseIntentDecision
		r, in, now = quarantineFixture(t)
		switch stage {
		case "restored":
			r.Execution.Recovery.Quarantine = nil
		case "quarantine_qualified":
			q := r.Execution.Recovery.Quarantine
			q.Decision, q.AuthorizerKeyID, q.DecidedAt = "", "", nil
		case "quarantine_applying", "quarantined":
			r, err = tasktoolrelease.Quarantine(r, in, true, now)
			if err == nil && stage == "quarantined" {
				in.Quarantine.Action = "observe"
				r, err = tasktoolrelease.Quarantine(r, in, true, now)
			}
		}
	default:
		var in api.TaskToolReleaseIntentDecision
		r, in, now = retirementFixture(t)
		switch stage {
		case "retirement_qualified":
			q := r.Execution.Recovery.Retirement
			q.Decision, q.AuthorizerKeyID, q.DecidedAt = "", "", nil
		case "retirement_pending", "retired":
			r, err = tasktoolrelease.Retirement(r, in, true, now, uuid.NewString())
			if err == nil && stage == "retired" {
				in.Retirement.Action = "observe"
				r, err = tasktoolrelease.Retirement(r, in, true, now, "")
			}
		case "retirement_approved":
		default:
			t.Fatal("unknown lifecycle stage", stage)
		}
	}
	r.Execution.CompletedStages = tasktoolrelease.ReceiptStages(*r.Execution.Receipt)
	if err != nil || !tasktoolrelease.ValidRecoveryMetadata(r) {
		t.Fatal("invalid lifecycle fixture", stage, err)
	}
	return r, now
}

// This fixture exposes only native reads. Task SQL, local files and workload
// mutations are unavailable, including when native zero-live has been observed.
func recoveryLifecycleBackend(t *testing.T, record api.TaskToolReleaseIntentRecord, now time.Time, mode string) *httptest.Server {
	t.Helper()
	var reads atomic.Int32
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != "GET" {
			t.Errorf("inspection mutated %s", req.URL.Path)
			w.WriteHeader(500)
			return
		}
		var out any
		r := record.Execution.Receipt
		switch {
		case strings.HasPrefix(req.URL.Path, "/v1/task-tool-release-intents/"):
			out = record
			if reads.Add(1) > 1 && mode == "journal_changed" {
				copy := record
				e := *record.Execution
				e.Stage = "prepared"
				copy.Execution = &e
				out = copy
			}
		case strings.HasPrefix(req.URL.Path, "/v1/deployments/"):
			id := strings.TrimPrefix(req.URL.Path, "/v1/deployments/")
			pin := record.Intent.Apps[0]
			role := "baseline"
			for _, p := range record.Intent.Apps {
				if p.Role == "web" {
					pin = p
				}
				if id == r.WorkerDeploymentID && p.Role == "worker" {
					pin = p
					role = "worker"
					break
				}
			}
			if id == r.WebDeploymentID {
				role = "web"
			}
			reason := ""
			if role != "baseline" {
				reason = tasktoolrelease.SubmissionReason(r.ReleaseID, role)
			}
			out = api.DeploymentResponse{ID: id, AppID: pin.Review.AppID, Status: "live", Scope: api.DefaultEnvScope, Reason: reason}
		case strings.HasSuffix(req.URL.Path, "/custom-metrics"):
			out = api.CustomMetricListResponse{FreshnessS: api.CustomMetricFreshnessSeconds, Metrics: []api.CustomMetricResponse{{Name: "mcp_tasks_observer_heartbeat", Value: 1, ObservedAt: now.Add(-time.Second), Stale: mode == "stale_observer"}}}
		default:
			found := false
			for _, pin := range record.Intent.Apps {
				path := "/v1/apps/" + pin.App
				generation := pin.Review.LatestDeploymentID
				if pin.Role == "web" {
					generation = r.WebDeploymentID
				}
				if pin.Role == "worker" {
					generation = r.WorkerDeploymentID
					if mode == "new_generation" {
						generation = uuid.NewString()
					}
				}
				if pin.Role == "previous_worker" {
					generation = r.PreviousDeployments[pin.App]
				}
				if req.URL.Path == path+"/release-review" {
					found = true
					if mode == "app_unavailable" {
						w.WriteHeader(503)
						return
					}
					review := pin.Review
					review.LatestDeploymentID = generation
					if pin.Role == "web" {
						serving := r.WebDeploymentID
						if record.Execution.Recovery != nil && record.Execution.Recovery.Result != nil {
							serving = r.ServingDeploymentID
						}
						if mode == "foreign_traffic" {
							serving = uuid.NewString()
						}
						review.ServingDeploymentIDs = []string{serving}
					}
					if mode == "configuration_changed" {
						review.ConfigurationHash = strings.Repeat("0", 64)
					}
					out = review
					break
				}
				if req.URL.Path == path+"/instances" {
					found = true
					rows := []api.InstanceResponse{{ID: uuid.NewString(), AppID: pin.Review.AppID, DeploymentID: generation, State: "running"}}
					if pin.Role == "worker" && (mode == "zero_live" || record.Execution.Recovery != nil && record.Execution.Recovery.Retirement != nil && record.Execution.Recovery.Retirement.Result != nil) {
						rows = []api.InstanceResponse{}
					}
					out = rows
					break
				}
			}
			if !found {
				t.Errorf("unexpected native read %s", req.URL.Path)
				w.WriteHeader(404)
				return
			}
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
}
func TestMCPTaskRecoveryLifecycleGuidance(t *testing.T) {
	for _, tc := range []struct {
		stage, classification, next string
		bindings                    bool
	}{
		{"handover_pending", "handover_pending", "observe_handover", false},
		{"executor_exited", "executor_exited", "request_handover", false},
		{"executor_fenced", "executor_fenced", "qualify_restore", true},
		{"restore_qualified", "executor_fenced", "approve_restore", false},
		{"restore_approved", "executor_fenced", "apply_restore", true},
		{"restored", "restored", "qualify_quarantine", true},
		{"quarantine_qualified", "restored", "approve_quarantine", false},
		{"quarantine_approved", "restored", "apply_quarantine", true},
		{"quarantine_applying", "quarantine_applying", "observe_quarantine", true},
		{"quarantined", "quarantined", "qualify_retirement", true},
		{"retirement_qualified", "quarantined", "approve_retirement", false},
		{"retirement_approved", "quarantined", "apply_retirement", true},
		{"retirement_pending", "retirement_pending", "observe_retirement", true},
		{"retired", "retired", "prepare_new_release", false},
	} {
		t.Run(tc.stage, func(t *testing.T) {
			record, now := recoveryLifecycleFixture(t, tc.stage)
			before, _ := json.Marshal(record)
			backend := recoveryLifecycleBackend(t, record, now, "")
			defer backend.Close()
			for _, plan := range []bool{false, true} {
				report, err := tasktoolrelease.InspectRecovery(t.Context(), api.NewClient(backend.URL, "private-token-sentinel"), record.ID, plan, now)
				if err != nil {
					t.Fatal(err)
				}
				if report.Classification != tc.classification || report.Lifecycle == nil || !report.Lifecycle.ExecutorFenced || report.NextStep == nil || report.NextStep.Action != tc.next || report.NextStep.RequiresOriginalBindings != tc.bindings || len(report.NextStep.Blockers) != 0 {
					t.Fatalf("unexpected lifecycle guidance %+v next=%+v checks=%+v", report, report.NextStep, report.Checks)
				}
				if !report.ReadOnly || report.ReadyToApply || !report.OwnerAuthorizationRequired || !toolpublication.ValidID(report.EvidenceSHA256) || !report.ExpiresAt.Equal(now.Add(api.TaskToolRecoveryObservationTTL)) {
					t.Fatal("report granted authority")
				}
				for _, a := range report.Actions {
					if a.Action == "resume" && (a.Eligibility != "blocked" || !slices.Contains(a.Blockers, "executor_permanently_fenced")) {
						t.Fatal("fenced executor can resume", a)
					}
					if record.Execution.Recovery != nil && record.Execution.Recovery.Result != nil && a.Action == "restore" && !slices.Contains(a.Blockers, "restore_already_recorded") {
						t.Fatal("committed restore can repeat", a)
					}
				}
				raw, _ := json.Marshal(report)
				if strings.Contains(string(raw), "private-token-sentinel") || strings.Contains(string(raw), record.Execution.ClaimSHA256) {
					t.Fatal("private claim metadata leaked")
				}
			}
			after, _ := json.Marshal(record)
			if string(before) != string(after) {
				t.Fatal("inspection rewrote its journal")
			}
		})
	}
}
func TestMCPTaskRecoveryLifecycleUncertaintyAndExpiredAuthority(t *testing.T) {
	for _, tc := range []struct {
		stage, mode, decision, next string
		expired                     bool
	}{
		{"restore_approved", "", "", "owner_reconciliation", true},
		{"quarantine_approved", "", "revoked", "owner_reconciliation", false},
		{"retirement_approved", "", "", "owner_reconciliation", true},
		{"quarantine_applying", "", "revoked", "observe_quarantine", true},
		{"retirement_pending", "zero_live", "revoked", "observe_retirement", true},
		{"retirement_pending", "new_generation", "", "owner_reconciliation", false},
		{"retirement_pending", "journal_changed", "", "inspect_again", false},
		{"restored", "foreign_traffic", "", "owner_reconciliation", false},
		{"quarantined", "configuration_changed", "", "owner_reconciliation", false},
		{"quarantined", "app_unavailable", "", "owner_reconciliation", false},
		{"quarantined", "stale_observer", "", "owner_reconciliation", false},
		{"retired", "new_generation", "revoked", "prepare_new_release", true},
	} {
		t.Run(tc.stage+"/"+tc.mode+"/"+tc.decision, func(t *testing.T) {
			record, now := recoveryLifecycleFixture(t, tc.stage)
			h := record.Execution.Recovery
			if tc.decision != "" {
				if h.Retirement != nil {
					h.Retirement.Decision = tc.decision
				} else if h.Quarantine != nil {
					h.Quarantine.Decision = tc.decision
				} else {
					h.Decision = tc.decision
				}
			}
			if tc.expired {
				now = now.Add(api.TaskToolRecoveryApprovalTTL)
			}
			backend := recoveryLifecycleBackend(t, record, now, tc.mode)
			defer backend.Close()
			report, err := tasktoolrelease.InspectRecovery(t.Context(), api.NewClient(backend.URL, "fixture"), record.ID, true, now)
			if err != nil {
				t.Fatal(err)
			}
			if report.NextStep == nil || report.NextStep.Action != tc.next {
				t.Fatalf("wrong next step %+v, facts=%+v", report.NextStep, report.Checks)
			}
			if tc.next == "owner_reconciliation" && len(report.NextStep.Blockers) == 0 {
				t.Fatal("missing blocker")
			}
			if tc.stage == "retirement_pending" && report.Classification != "retirement_pending" {
				t.Fatal("native zero-live certified Task shutdown")
			}
			for _, a := range report.Actions {
				if a.Action == "resume" && a.Eligibility != "blocked" {
					t.Fatal("fence lost after expiry")
				}
				if tc.stage == "retired" && a.Eligibility != "blocked" {
					t.Fatal("completed cleanup can reapply", a)
				}
			}
		})
	}
}
func TestMCPTaskRecoveryRejectsMalformedLifecycleMetadata(t *testing.T) {
	for _, change := range []string{"fence_version", "handover", "revision", "unsettled", "qualification_apps", "restore_digest", "quarantine_digest", "worker_ids", "retirement_transition", "unknown_state", "completed_stages"} {
		t.Run(change, func(t *testing.T) {
			r, now := recoveryLifecycleFixture(t, "retired")
			h := r.Execution.Recovery
			switch change {
			case "fence_version":
				r.Execution.FenceVersion = 2
			case "handover":
				h.ID = strings.Repeat("0", 64)
			case "revision":
				h.Revision++
			case "unsettled":
				h.SettledAt = nil
			case "qualification_apps":
				h.Qualification.NativeApps[0].Review.AppID = uuid.NewString()
			case "restore_digest":
				h.Result.QualificationSHA256 = strings.Repeat("0", 64)
			case "quarantine_digest":
				h.Quarantine.Result.QualificationSHA256 = strings.Repeat("0", 64)
			case "worker_ids":
				h.Quarantine.Result.WorkerIDs = []string{uuid.NewString()}
			case "retirement_transition":
				h.Retirement.Result.ParkTransitionID = uuid.NewString()
			case "unknown_state":
				h.Retirement.State = "future_state"
			case "completed_stages":
				r.Execution.CompletedStages = nil
			}
			var reads atomic.Int32
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				reads.Add(1)
				if req.Method != "GET" || req.URL.Path != "/v1/task-tool-release-intents/"+r.ID {
					t.Error("invalid metadata reached observation")
				}
				_ = json.NewEncoder(w).Encode(r)
			}))
			defer backend.Close()
			_, err := tasktoolrelease.InspectRecovery(t.Context(), api.NewClient(backend.URL, "fixture"), r.ID, true, now)
			if !errors.Is(err, tasktoolrelease.ErrInvalid) || reads.Load() != 1 {
				t.Fatal("malformed lifecycle was trusted", err, reads.Load())
			}
		})
	}
}

func TestMCPTaskRecoveryFutureTimelineCannotRecommendApply(t *testing.T) {
	r, now := recoveryLifecycleFixture(t, "retirement_pending")
	observed := now.Add(-time.Second)
	backend := recoveryLifecycleBackend(t, r, observed, "")
	defer backend.Close()
	report, err := tasktoolrelease.InspectRecovery(t.Context(), api.NewClient(backend.URL, "fixture"), r.ID, true, observed)
	if err != nil || report.NextStep == nil || report.NextStep.Action != "inspect_again" || !slices.Contains(report.NextStep.Blockers, "recovery_timeline_unverified") {
		t.Fatal("future journal offered action", err, report.NextStep)
	}
	for _, a := range report.Actions {
		if a.Eligibility != "blocked" || !slices.Contains(a.Blockers, "recovery_timeline_unverified") {
			t.Fatal("future metadata gave live authority", a)
		}
	}
}

func TestMCPTaskRecoveryExitedWithoutCheckpointRequiresOwnerReconciliation(t *testing.T) {
	r, now := recoveryLifecycleFixture(t, "executor_exited")
	r.Execution.Receipt = nil
	r.Execution.CompletedStages = nil
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != "GET" || req.URL.Path != "/v1/task-tool-release-intents/"+r.ID {
			t.Error("missing checkpoint caused other work")
		}
		_ = json.NewEncoder(w).Encode(r)
	}))
	defer backend.Close()
	report, err := tasktoolrelease.InspectRecovery(t.Context(), api.NewClient(backend.URL, "fixture"), r.ID, true, now)
	if err != nil || report.Classification != "executor_exited" || report.NextStep == nil || report.NextStep.Action != "owner_reconciliation" || !slices.Contains(report.NextStep.Blockers, "checkpoint_missing") {
		t.Fatal("missing checkpoint suggested retrying the executor", err, report.NextStep)
	}
	for _, a := range report.Actions {
		if a.Eligibility != "blocked" {
			t.Fatal("missing checkpoint offered an eligible mutation", a)
		}
	}
}
