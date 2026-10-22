package state

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestProjectServiceAdditionsMemory(t *testing.T) {
	testProjectServiceAdditions(t, func(*testing.T) Store { return NewMemStore() })
}

func testProjectServiceAdditions(t *testing.T, factory func(*testing.T) Store) {
	for _, outcome := range []string{"activate", "failed", "not_ready", "rollback", "operator", "changed_settings", "missing_addition", "unregistered_candidate"} {
		t.Run(outcome, func(t *testing.T) {
			store := factory(t)
			ctx := t.Context()
			require := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			account, err := store.CreateAccount(ctx, "additions@example.test", api.PlanPro)
			require(err)
			project, err := store.CreateProject(ctx, Project{AccountID: account.ID, Slug: "shop"})
			require(err)
			web, err := store.CreateApp(ctx, App{AccountID: account.ID, ProjectID: project.ID, Slug: "web", WorkloadName: "web", WorkloadClass: WorkloadClassHTTP, Manifest: AppManifest{RevisionPinTTLSeconds: 1800}})
			require(err)
			old, err := store.CreateDeployment(ctx, Deployment{AppID: web.ID, Scope: "production", Kind: DeploymentKindTarball})
			require(err)
			require(store.MarkDeploymentLive(ctx, old.ID))
			publisher := store.(ProjectReleaseSetStore)
			reader := store.(ProjectReleaseSetReader)
			reservations := store.(ProjectReleaseAdditionsStore)
			submissions := store.(ProjectSubmissionStore)
			worker := store.(ProjectSafeReleaseStore)
			original := []ProjectReleaseMember{{AppID: web.ID, DeploymentID: old.ID}}
			baseline, err := publisher.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, original)
			require(err)
			draft := App{AccountID: account.ID, ProjectID: project.ID, Slug: "metrics", WorkloadName: "metrics", RootDir: "metrics", WorkloadClass: WorkloadClassHTTP, PublicAuthMode: api.AppPublicAuthModeOpen, Manifest: AppManifest{RevisionPinTTLSeconds: 1800}}
			safe := &api.ProjectSafeRelease{AddServices: true, Status: "building", PreviousReleaseID: baseline.ID, TTLSeconds: 1800, Deadline: time.Now().Add(time.Hour), Additions: []api.ProjectReleaseAddition{{Slug: draft.Slug, RootDir: draft.RootDir, Fingerprint: ProjectReleaseAdditionFingerprint(draft)}}}
			if outcome == "rollback" {
				safe.Verification = &api.ProjectReleaseVerification{Probes: []api.ProjectReleaseProbe{{AppID: web.ID, Slug: web.Slug, Method: "GET", Path: "/health"}}}
			}
			attempt := strings.Repeat("a", 32)
			receipt := api.ProjectSubmissionReceipt{AttemptID: attempt, ProjectSlug: project.Slug, Environment: "production", Status: "processing", Apply: api.ApplyResponse{ProjectID: project.ID, PlanResponse: api.PlanResponse{SafeRelease: safe}}}
			_, err = submissions.ReserveProjectSubmission(ctx, account.ID, attempt, strings.Repeat("b", 64), receipt)
			require(err)
			apps, err := reservations.StageProjectReleaseAdditions(ctx, account.ID, attempt, project.ID, safe, []App{draft})
			require(err)
			added := apps[0]
			checkpoint, err := submissions.ProjectSubmission(ctx, account.ID, attempt)
			require(err)
			if checkpoint.Status != "processing" || len(checkpoint.Apply.SafeRelease.Additions) != 1 || checkpoint.Apply.SafeRelease.Additions[0].AppID != added.ID {
				t.Fatal("reservation ownership was not checkpointed atomically")
			}
			safe.Additions[0].AppID = added.ID
			// Reservation and lost-response retries keep the same app identity.
			again, err := reservations.StageProjectReleaseAdditions(ctx, account.ID, attempt, project.ID, safe, apps)
			require(err)
			if len(again) != 1 || again[0].ID != added.ID {
				t.Fatal("reservation was not idempotent")
			}
			if _, _, err := publisher.ResolveProjectRelease(ctx, added.ID, "production", ""); !errors.Is(err, ErrConflict) {
				t.Fatalf("new service exposed before publication: %v", err)
			}
			if _, err := store.CreateDeployment(ctx, Deployment{AppID: added.ID, Scope: "production", Kind: DeploymentKindTarball, TrafficPercentExplicit: true}); !errors.Is(err, ErrConflict) {
				t.Fatalf("unrelated deploy took over a reserved addition: %v", err)
			}
			if err := store.(ProjectReleaseSetPromotionStore).DeactivateProjectReleaseSetIfActive(ctx, account.ID, project.ID, "production", baseline.ID, original); !errors.Is(err, ErrConflict) {
				t.Fatalf("weighted fallback exposed a reserved app: %v", err)
			}
			receipt.Status, receipt.SelectionComplete = "complete", true
			buildApps := []App{web, added}
			if outcome == "unregistered_candidate" {
				other, err := store.CreateApp(ctx, App{AccountID: account.ID, ProjectID: project.ID, Slug: "unregistered", WorkloadName: "unregistered", Manifest: AppManifest{RevisionPinTTLSeconds: 1800}})
				require(err)
				buildApps = append(buildApps, other)
			}
			for _, app := range buildApps {
				started := time.Now()
				stages, err := json.Marshal(StageState{Current: StageReadiness, CurrentStartedAt: &started})
				require(err)
				dep, err := store.CreateDeployment(ContextWithDeploymentSubmission(ctx, ProjectWorkloadAttempt(attempt, app.ID)), Deployment{AppID: app.ID, Scope: "production", Kind: DeploymentKindTarball, TrafficPercentExplicit: true, StageState: stages})
				require(err)
				if outcome == "failed" && app.ID == added.ID {
					require(store.UpdateDeploymentStatus(ctx, dep.ID, DeployFailed, ""))
				} else {
					require(store.MarkDeploymentLive(ctx, dep.ID))
					if outcome != "not_ready" || app.ID != added.ID {
						_, err := store.CloseDeploymentStage(ctx, dep.ID, StageReadiness, time.Now())
						require(err)
					}
				}
				receipt.Apply.Builds = append(receipt.Apply.Builds, api.AppliedBuild{Slug: app.Slug, AppID: app.ID, DeploymentID: dep.ID, BuildID: "build"})
				if app.ID == added.ID {
					if _, err := store.(RevisionPinStore).ResolveRevisionPin(ctx, app.ID, "production", dep.ID); !errors.Is(err, ErrNotFound) {
						t.Fatalf("dark addition exposed through revision pin: %v", err)
					}
					if _, _, err := publisher.ResolveServiceRelease(ctx, web.ID, old.ID, added.ID, baseline.ID); err == nil {
						t.Fatal("baseline service call reached an unpublished addition")
					}
				}
			}
			if outcome == "missing_addition" {
				receipt.Apply.Builds = receipt.Apply.Builds[:1]
			}
			if outcome == "changed_settings" {
				command := "changed"
				_, err := store.UpdateApp(ctx, added.ID, UpdateAppParams{StartCommand: &command})
				require(err)
			}
			if outcome == "operator" {
				_, err := publisher.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, original)
				require(err)
			}
			require(submissions.WriteProjectSubmission(ctx, account.ID, receipt))
			progress, err := worker.ReconcileSafeProjectSubmission(ctx, account.ID, attempt)
			require(err)
			active, err := reader.ActiveProjectReleaseSet(ctx, account.ID, project.ID, "production")
			require(err)
			switch outcome {
			case "activate", "rollback":
				if active.ID == baseline.ID || len(active.Members) != 2 || progress.ReleaseID != active.ID {
					t.Fatalf("expanded graph not published: %+v %+v", progress, active)
				}
				if _, _, err := publisher.ResolveProjectRelease(ctx, added.ID, "production", ""); err != nil {
					t.Fatal(err)
				}
				if outcome == "rollback" {
					// Resume a persisted rollback checkpoint after an APID restart.
					receipt, err := submissions.ProjectSubmission(ctx, account.ID, attempt)
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
					if len(graph.Members) != 1 || graph.Members[0] != original[0] {
						t.Fatal("rollback did not restore exact membership")
					}
					if _, _, err := publisher.ResolveProjectRelease(ctx, added.ID, "production", ""); !errors.Is(err, ErrConflict) {
						t.Fatal("rolled-back addition still has a default route")
					}
					if _, _, err := publisher.ResolveProjectRelease(ctx, added.ID, "production", active.ID); err != nil {
						t.Fatalf("retained expanded graph lost its service: %v", err)
					}
				}
			case "not_ready":
				if progress.Status != "building" || active.ID != baseline.ID {
					t.Fatal("published without new service readiness")
				}
			default:
				if progress.Status != "failed" && progress.Status != "conflicted" {
					t.Fatalf("unexpected outcome: %+v", progress)
				}
				if len(active.Members) != 1 {
					t.Fatal("failed addition changed graph")
				}
			}
			if outcome == "failed" || outcome == "rollback" {
				// Dormant additions neither prevent ordinary source releases nor
				// require a new slug/app ID to retry the intended service.
				latest, err := publisher.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, original)
				require(err)
				safe.PreviousReleaseID, safe.Status, safe.ReleaseID = latest.ID, "building", ""
				attempt2 := strings.Repeat("c", 32)
				receipt.AttemptID, receipt.Status = attempt2, "processing"
				receipt.Apply.SafeRelease = safe
				_, err = submissions.ReserveProjectSubmission(ctx, account.ID, attempt2, strings.Repeat("d", 64), receipt)
				require(err)
				retried, err := reservations.StageProjectReleaseAdditions(ctx, account.ID, attempt2, project.ID, safe, apps)
				require(err)
				if retried[0].ID != added.ID {
					t.Fatal("retry created a second app")
				}
			}
		})
	}
}

func TestProjectServiceAdditionQuotaMemory(t *testing.T) {
	testProjectServiceAdditionQuota(t, NewMemStore())
}

func testProjectServiceAdditionQuota(t *testing.T, store Store) {
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "quota-additions@example.test", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, Project{AccountID: account.ID, Slug: "quota"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, App{AccountID: account.ID, ProjectID: project.ID, Slug: "quota-web", WorkloadName: "web", Manifest: AppManifest{RevisionPinTTLSeconds: 1800}})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, Scope: "production", Kind: DeploymentKindTarball})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	baseline, err := store.(ProjectReleaseSetStore).PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, []ProjectReleaseMember{{AppID: app.ID, DeploymentID: dep.ID}})
	if err != nil {
		t.Fatal(err)
	}
	safe := &api.ProjectSafeRelease{AddServices: true, Status: "building", PreviousReleaseID: baseline.ID, TTLSeconds: 1800}
	var drafts []App
	for _, name := range []string{"quota-api", "quota-metrics"} {
		draft := App{AccountID: account.ID, ProjectID: project.ID, Slug: name, WorkloadName: name, WorkloadClass: WorkloadClassHTTP, PublicAuthMode: api.AppPublicAuthModeOpen, Manifest: AppManifest{RevisionPinTTLSeconds: 1800}}
		drafts = append(drafts, draft)
		safe.Additions = append(safe.Additions, api.ProjectReleaseAddition{Slug: name, Fingerprint: ProjectReleaseAdditionFingerprint(draft)})
	}
	attempt := strings.Repeat("e", 32)
	receipt := api.ProjectSubmissionReceipt{AttemptID: attempt, Status: "processing", Apply: api.ApplyResponse{ProjectID: project.ID, PlanResponse: api.PlanResponse{SafeRelease: safe}}}
	_, err = store.(ProjectSubmissionStore).ReserveProjectSubmission(ctx, account.ID, attempt, strings.Repeat("f", 64), receipt)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.(ProjectReleaseAdditionsStore).StageProjectReleaseAdditions(ctx, account.ID, attempt, project.ID, safe, drafts)
	var quota *QuotaError
	if !errors.As(err, &quota) {
		t.Fatalf("missing authoritative quota rejection: %v", err)
	}
	apps, err := store.AppsForProject(ctx, account.ID, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 1 {
		t.Fatal("failed batch leaked app reservations")
	}
}
