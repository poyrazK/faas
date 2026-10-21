package state

// adr: 20261021120000

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestProjectServiceRemovalsMemory(t *testing.T) {
	testProjectServiceRemovals(t, func(*testing.T) Store { return NewMemStore() })
}

func testProjectServiceRemovals(t *testing.T, factory func(*testing.T) Store) {
	for _, outcome := range []string{"retire", "rollback", "changed_settings", "not_ready"} {
		t.Run(outcome, func(t *testing.T) {
			store := factory(t)
			ctx := t.Context()
			require := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			account, err := store.CreateAccount(ctx, "removals@example.test", api.PlanPro)
			require(err)
			project, err := store.CreateProject(ctx, Project{AccountID: account.ID, Slug: "shop"})
			require(err)
			var apps []App
			var original []ProjectReleaseMember
			for _, name := range []string{"web", "legacy"} {
				app, err := store.CreateApp(ctx, App{AccountID: account.ID, ProjectID: project.ID, Slug: name, WorkloadName: name, RootDir: name, WorkloadClass: WorkloadClassHTTP, Manifest: AppManifest{RevisionPinTTLSeconds: 1800}})
				require(err)
				dep, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, Scope: "production", Kind: DeploymentKindTarball})
				require(err)
				require(store.MarkDeploymentLive(ctx, dep.ID))
				apps = append(apps, app)
				original = append(original, ProjectReleaseMember{AppID: app.ID, DeploymentID: dep.ID})
			}
			web, legacy := apps[0], apps[1]
			publisher := store.(ProjectReleaseSetStore)
			reader := store.(ProjectReleaseSetReader)
			retirements := store.(ProjectReleaseRemovalsStore)
			submissions := store.(ProjectSubmissionStore)
			worker := store.(ProjectSafeReleaseStore)
			baseline, err := publisher.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, original)
			require(err)
			safe := &api.ProjectSafeRelease{RemoveServices: true, Status: "building", PreviousReleaseID: baseline.ID, TTLSeconds: 1800, Deadline: time.Now().Add(time.Hour),
				Removals: []api.ProjectReleaseRemoval{{AppID: legacy.ID, Slug: legacy.Slug, RootDir: legacy.RootDir, Fingerprint: ProjectReleaseAdditionFingerprint(legacy)}}}
			if outcome == "rollback" {
				safe.Verification = &api.ProjectReleaseVerification{Probes: []api.ProjectReleaseProbe{{AppID: web.ID, Slug: web.Slug, Method: "GET", Path: "/health"}}}
			}
			attempt := strings.Repeat("a", 32)
			receipt := api.ProjectSubmissionReceipt{AttemptID: attempt, ProjectSlug: project.Slug, Environment: "production", Status: "processing", Apply: api.ApplyResponse{ProjectID: project.ID, PlanResponse: api.PlanResponse{SafeRelease: safe}}}
			_, err = submissions.ReserveProjectSubmission(ctx, account.ID, attempt, strings.Repeat("b", 64), receipt)
			require(err)
			stale := *safe
			stale.Removals = []api.ProjectReleaseRemoval{{AppID: legacy.ID, Slug: legacy.Slug, RootDir: legacy.RootDir, Fingerprint: strings.Repeat("0", 64)}}
			if err := retirements.StageProjectReleaseRemovals(ctx, account.ID, attempt, project.ID, &stale); !errors.Is(err, ErrConflict) {
				t.Fatalf("stale retirement plan staged: %v", err)
			}
			require(retirements.StageProjectReleaseRemovals(ctx, account.ID, attempt, project.ID, safe))
			// A lost response retries the same attempt.
			require(retirements.StageProjectReleaseRemovals(ctx, account.ID, attempt, project.ID, safe))
			if _, _, err := publisher.ResolveProjectRelease(ctx, legacy.ID, "production", ""); err != nil {
				t.Fatalf("service stopped serving before publication: %v", err)
			}
			started := time.Now()
			stages, err := json.Marshal(StageState{Current: StageReadiness, CurrentStartedAt: &started})
			require(err)
			candidate, err := store.CreateDeployment(ContextWithDeploymentSubmission(ctx, ProjectWorkloadAttempt(attempt, web.ID)), Deployment{AppID: web.ID, Scope: "production", Kind: DeploymentKindTarball, TrafficPercentExplicit: true, StageState: stages})
			require(err)
			require(store.MarkDeploymentLive(ctx, candidate.ID))
			if outcome != "not_ready" {
				_, err := store.CloseDeploymentStage(ctx, candidate.ID, StageReadiness, time.Now())
				require(err)
			}
			receipt.Status, receipt.SelectionComplete = "complete", true
			receipt.Apply.Builds = []api.AppliedBuild{{Slug: web.Slug, AppID: web.ID, DeploymentID: candidate.ID, BuildID: "build"}}
			if outcome == "changed_settings" {
				command := "changed"
				_, err := store.UpdateApp(ctx, legacy.ID, UpdateAppParams{StartCommand: &command})
				require(err)
			}
			require(submissions.WriteProjectSubmission(ctx, account.ID, receipt))
			progress, err := worker.ReconcileSafeProjectSubmission(ctx, account.ID, attempt)
			require(err)
			active, err := reader.ActiveProjectReleaseSet(ctx, account.ID, project.ID, "production")
			require(err)
			switch outcome {
			case "not_ready":
				if progress.Status != "building" || active.ID != baseline.ID {
					t.Fatal("published before the remaining services were ready")
				}
				return
			case "changed_settings":
				if progress.Status != "failed" && progress.Status != "conflicted" || active.ID != baseline.ID {
					t.Fatalf("retired a service whose settings changed: %+v", progress)
				}
				return
			}
			if active.ID == baseline.ID || len(active.Members) != 1 || active.Members[0].AppID != web.ID || progress.ReleaseID != active.ID {
				t.Fatalf("reduced graph not published: %+v %+v", progress, active)
			}
			if _, _, err := publisher.ResolveProjectRelease(ctx, legacy.ID, "production", ""); !errors.Is(err, ErrConflict) {
				t.Fatalf("retired service still has a default route: %v", err)
			}
			if _, _, err := publisher.ResolveProjectRelease(ctx, legacy.ID, "production", baseline.ID); err != nil {
				t.Fatalf("retained baseline lost the retired service: %v", err)
			}
			if _, err := store.CreateDeployment(ctx, Deployment{AppID: legacy.ID, Scope: "production", Kind: DeploymentKindTarball, TrafficPercentExplicit: true}); !errors.Is(err, ErrConflict) {
				t.Fatalf("an ordinary deploy revived a retired service: %v", err)
			}
			reduced := []ProjectReleaseMember{{AppID: web.ID, DeploymentID: candidate.ID}}
			if _, err := publisher.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, append(reduced, original[1])); !errors.Is(err, ErrConflict) {
				t.Fatalf("an ordinary publisher re-activated a retired service: %v", err)
			}
			if outcome == "retire" {
				// Later ordinary releases omit the retired service.
				_, err := publisher.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, reduced)
				require(err)
				return
			}
			receipt, err = submissions.ProjectSubmission(ctx, account.ID, attempt)
			require(err)
			receipt.Apply.SafeRelease.Status = "rolling_back"
			SeedProjectVerificationForTest(t, store, account.ID, receipt)
			restored, err := store.(ProjectReleaseVerificationStore).AdvanceProjectReleaseVerification(ctx, account.ID, attempt, nil)
			require(err)
			if restored.Status != "rolled_back" {
				t.Fatalf("rollback: %+v", restored)
			}
			graph, err := reader.ActiveProjectReleaseSet(ctx, account.ID, project.ID, "production")
			require(err)
			if len(graph.Members) != 2 {
				t.Fatalf("rollback did not restore the retired service: %+v", graph.Members)
			}
			if _, _, err := publisher.ResolveProjectRelease(ctx, legacy.ID, "production", ""); err != nil {
				t.Fatalf("restored service has no default route: %v", err)
			}
			// Serving again, it must take part in ordinary releases.
			if _, err := publisher.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, reduced); !errors.Is(err, ErrConflict) {
				t.Fatalf("ordinary publisher dropped a restored service: %v", err)
			}
		})
	}
}
