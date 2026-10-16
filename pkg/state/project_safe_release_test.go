package state_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSafeProjectReleaseMemory(t *testing.T) {
	testSafeProjectRelease(t, func(t *testing.T) state.Store { return state.NewMemStore() })
}
func testSafeProjectRelease(t *testing.T, newStore func(*testing.T) state.Store) {
	t.Helper()
	for _, outcome := range []string{"success", "failed", "operator", "not_ready", "expired", "partial", "verified_success", "verified_rollback", "verified_operator", "verified_timeout", "verified_both_fail"} {
		t.Run(outcome, func(t *testing.T) {
			store := newStore(t)
			ctx := t.Context()
			account, err := store.CreateAccount(ctx, "safe@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "safe"})
			if err != nil {
				t.Fatal(err)
			}
			publisher := store.(state.ProjectReleaseSetStore)
			reader := store.(state.ProjectReleaseSetReader)
			worker := store.(state.ProjectSafeReleaseStore)
			receipts := store.(state.ProjectSubmissionStore)
			var oldMembers []state.ProjectReleaseMember
			var apps []state.App
			for _, name := range []string{"api", "web"} {
				app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "safe-" + name, WorkloadName: name, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
				if err != nil {
					t.Fatal(err)
				}
				old, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindTarball})
				if err != nil {
					t.Fatal(err)
				}
				if err := store.MarkDeploymentLive(ctx, old.ID); err != nil {
					t.Fatal(err)
				}
				oldMembers = append(oldMembers, state.ProjectReleaseMember{AppID: app.ID, DeploymentID: old.ID})
				apps = append(apps, app)
			}
			baseline, err := publisher.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, oldMembers)
			if err != nil {
				t.Fatal(err)
			}
			safe := &api.ProjectSafeRelease{Status: "building", PreviousReleaseID: baseline.ID, TTLSeconds: 1800, Deadline: time.Now().Add(time.Hour)}
			if strings.HasPrefix(outcome, "verified_") {
				safe.Verification = &api.ProjectReleaseVerification{Probes: []api.ProjectReleaseProbe{{AppID: apps[0].ID, Slug: apps[0].Slug, Method: "GET", Path: "/health"}}}
			}
			if outcome == "expired" {
				safe.Deadline = time.Now().Add(-time.Minute)
			}
			attempt := strings.Repeat("a", 32)
			receipt := api.ProjectSubmissionReceipt{AttemptID: attempt, ProjectSlug: project.Slug, SelectionComplete: true, Status: "complete", Apply: api.ApplyResponse{ProjectID: project.ID, PlanResponse: api.PlanResponse{SafeRelease: safe}}}
			for i, app := range apps {
				now := time.Now()
				initial, _ := json.Marshal(state.StageState{Current: state.StageReadiness, CurrentStartedAt: &now})
				candidate, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindTarball, TrafficPercentExplicit: true, StageState: initial})
				if err != nil {
					t.Fatal(err)
				}
				if outcome == "failed" && i == 1 {
					if err := store.UpdateDeploymentStatus(ctx, candidate.ID, state.DeployFailed, ""); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := store.MarkDeploymentLive(ctx, candidate.ID); err != nil {
						t.Fatal(err)
					}
					if outcome != "not_ready" || i == 0 {
						if _, err := store.CloseDeploymentStage(ctx, candidate.ID, state.StageReadiness, time.Now()); err != nil {
							t.Fatal(err)
						}
					}
				}
				receipt.Apply.Builds = append(receipt.Apply.Builds, api.AppliedBuild{Slug: app.Slug, AppID: app.ID, DeploymentID: candidate.ID, BuildID: "accepted-build"})
			}
			if outcome == "partial" {
				receipt.Apply.Builds[1].DeploymentID = ""
				receipt.Apply.Builds[1].Error = "admission failed"
			}
			if _, err := receipts.ReserveProjectSubmission(ctx, account.ID, attempt, strings.Repeat("c", 64), receipt); err != nil {
				t.Fatal(err)
			}
			if err := receipts.WriteProjectSubmission(ctx, account.ID, receipt); err != nil {
				t.Fatal(err)
			}
			expected := baseline.ID
			if outcome == "operator" {
				operator, err := publisher.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, oldMembers)
				if err != nil {
					t.Fatal(err)
				}
				expected = operator.ID
			}
			progress, err := worker.ReconcileSafeProjectSubmission(ctx, account.ID, attempt)
			if err != nil {
				t.Fatal(err)
			}
			active, err := reader.ActiveProjectReleaseSet(ctx, account.ID, project.ID, "production")
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(outcome, "verified_") {
				testVerifiedProjectRelease(t, store, account.ID, attempt, project.ID, baseline, progress, outcome)
				return
			}
			if outcome == "success" {
				if progress.Status != "activated" || active.ID == baseline.ID || active.ID != progress.ReleaseID {
					t.Fatalf("progress=%+v active=%+v", progress, active)
				}
				for _, app := range apps {
					_, target, err := publisher.ResolveProjectRelease(ctx, app.ID, "production", "")
					if err != nil {
						t.Fatal(err)
					}
					if target == oldMembers[0].DeploymentID || target == oldMembers[1].DeploymentID {
						t.Fatal("mixed graph after cutover")
					}
				}
				again, err := worker.ReconcileSafeProjectSubmission(ctx, account.ID, attempt)
				if err != nil || again.ReleaseID != progress.ReleaseID {
					t.Fatalf("restart duplicated activation: %+v %v", again, err)
				}
			} else {
				if active.ID != expected {
					t.Fatalf("failure switched production: %s -> %s", expected, active.ID)
				}
				wanted := "failed"
				if outcome == "operator" {
					wanted = "conflicted"
				}
				if outcome == "not_ready" {
					wanted = "building"
				}
				if progress.Status != wanted {
					t.Fatalf("status=%s want=%s", progress.Status, wanted)
				}
			}
		})
	}
}

func testVerifiedProjectRelease(t *testing.T, store state.Store, accountID, attempt, projectID string, baseline state.ProjectReleaseSet, progress *api.ProjectSafeRelease, outcome string) {
	t.Helper()
	ctx := t.Context()
	if progress.Status != "verifying" || progress.Verification.Deadline == nil || progress.ReleaseID == baseline.ID {
		t.Fatalf("cutover not verifying: %+v", progress)
	}
	worker := store.(state.ProjectReleaseVerificationStore)
	receipts := store.(state.ProjectSubmissionStore)
	reader := store.(state.ProjectReleaseSetReader)
	jobs, err := store.(state.ProjectSafeReleaseStore).ListPendingSafeProjectSubmissions(ctx)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("verification not rediscovered: %v %v", jobs, err)
	}
	r, err := receipts.ProjectSubmission(ctx, accountID, attempt)
	if err != nil {
		t.Fatal(err)
	}
	v := r.Apply.SafeRelease.Verification
	// Seed a persisted first round one minute ago to exercise restart recovery
	// without wall-clock sleeps. Subsequent transitions use the real store API.
	past := time.Now().Add(-2 * time.Minute)
	v.CheckedAt = &past
	v.HealthyRounds = 1
	if outcome == "verified_rollback" {
		v.HealthyRounds = 0
		v.RegressedRounds = 1
	}
	if outcome == "verified_timeout" || outcome == "verified_both_fail" {
		expired := time.Now().Add(-time.Second)
		v.Deadline = &expired
	}
	if outcome == "verified_both_fail" {
		v.Evidence = []api.ProjectReleaseProbeEvidence{{Probe: v.Probes[0], CandidateRequests: 10, CandidateErrors: 2, StableRequests: 10, StableErrors: 2}}
	}
	state.SeedProjectVerificationForTest(t, store, accountID, r)
	evidence := api.ProjectReleaseProbeEvidence{Probe: v.Probes[0], CandidateRequests: 10, StableRequests: 10}
	if outcome == "verified_rollback" {
		evidence.CandidateErrors = 3
	}
	round := &state.ProjectReleaseVerificationRound{PreviousCheckedAt: &past, Evidence: []api.ProjectReleaseProbeEvidence{evidence}}
	expectedActive := progress.ReleaseID
	if outcome == "verified_operator" {
		newer, err := store.(state.ProjectReleaseSetStore).PublishProjectReleaseSet(ctx, accountID, projectID, "production", 1800, baseline.Members)
		if err != nil {
			t.Fatal(err)
		}
		expectedActive = newer.ID
	}
	// A stale worker may not add another healthy round or settle the operation.
	if outcome == "verified_success" || outcome == "verified_rollback" {
		stale, err := worker.AdvanceProjectReleaseVerification(ctx, accountID, attempt, &state.ProjectReleaseVerificationRound{Evidence: round.Evidence})
		if err != nil || stale.Status != "verifying" {
			t.Fatalf("stale round counted: %+v %v", stale, err)
		}
	}
	result, err := worker.AdvanceProjectReleaseVerification(ctx, accountID, attempt, round)
	if err != nil {
		t.Fatal(err)
	}
	expected := "succeeded"
	if outcome == "verified_both_fail" {
		expected = "rollback_failed"
	}
	if outcome == "verified_operator" {
		expected = "conflicted"
	}
	if outcome == "verified_rollback" || outcome == "verified_timeout" {
		if result.Status != "rolling_back" {
			t.Fatalf("rollback intent not durable: %+v", result)
		}
		// Recreate discovery from the persisted receipt, as after an APID restart.
		loaded, err := receipts.ProjectSubmission(ctx, accountID, attempt)
		if err != nil || loaded.Apply.SafeRelease.Status != "rolling_back" {
			t.Fatalf("lost rollback intent: %v", err)
		}
		result, err = worker.AdvanceProjectReleaseVerification(ctx, accountID, attempt, nil)
		if err != nil {
			t.Fatal(err)
		}
		expected = "rolled_back"
		expectedActive = result.RestoredReleaseID
	}
	if result.Status != expected {
		t.Fatalf("status %s want %s", result.Status, expected)
	}
	active, err := reader.ActiveProjectReleaseSet(ctx, accountID, projectID, "production")
	if err != nil || active.ID != expectedActive {
		t.Fatalf("active=%+v expected=%s err=%v", active, expectedActive, err)
	}
	if expected == "rolled_back" {
		for _, old := range baseline.Members {
			_, id, err := store.(state.ProjectReleaseSetStore).ResolveProjectRelease(ctx, old.AppID, "production", "")
			if err != nil || id != old.DeploymentID {
				t.Fatalf("mixed rollback graph: %s %v", id, err)
			}
		}
	}
	again, err := worker.AdvanceProjectReleaseVerification(ctx, accountID, attempt, round)
	if err != nil || again.Status != expected || again.RestoredReleaseID != result.RestoredReleaseID {
		t.Fatalf("repeated transition changed release: %+v %v", again, err)
	}
	jobs, err = store.(state.ProjectSafeReleaseStore).ListPendingSafeProjectSubmissions(ctx)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("terminal attempt still pending: %v %v", jobs, err)
	}
}
func TestProjectReleaseProbeVerdict(t *testing.T) {
	probe := api.ProjectReleaseProbe{AppID: "api", Method: "GET", Path: "/health"}
	for _, tc := range []struct {
		name, want                                       string
		requests, errors, stableErrors, auth, unexpected int
	}{
		{"healthy", "healthy", 10, 0, 0, 0, 0}, {"regression", "regressed", 10, 2, 0, 0, 0}, {"sparse", "unknown", 9, 0, 0, 0, 0}, {"both failing", "unknown", 10, 2, 1, 0, 0}, {"candidate fixes baseline", "healthy", 10, 0, 1, 0, 0}, {"auth", "unknown", 10, 0, 0, 1, 0}, {"missing endpoint", "unknown", 10, 0, 0, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := api.ProjectReleaseProbeEvidence{Probe: probe, CandidateRequests: tc.requests, CandidateErrors: tc.errors, StableRequests: 10, StableErrors: tc.stableErrors, Unauthenticated: tc.auth, UnexpectedResponses: tc.unexpected}
			if got := state.ProjectReleaseProbeVerdict([]api.ProjectReleaseProbe{probe}, []api.ProjectReleaseProbeEvidence{e}); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
	if got := state.ProjectReleaseProbeVerdict([]api.ProjectReleaseProbe{probe}, nil); got != "unknown" {
		t.Fatal("missing evidence treated as healthy")
	}
}
