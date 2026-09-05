package state_test

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

func TestBuildCompletionContract(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var s state.Store = state.NewMemStore()
			ctx := context.Background()
			if backend == "postgres" {
				s, ctx = pgStore(t)
			}
			acct, err := s.CreateAccount(ctx, "completion@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "completion", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
			if err != nil {
				t.Fatal(err)
			}
			for _, scenario := range []string{"success", "cancelled", "reaped", "new-claim", "deployment-cancelled", "failure"} {
				t.Run(scenario, func(t *testing.T) {
					dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployBuilding})
					if err != nil {
						t.Fatal(err)
					}
					b, err := s.CreateBuild(ctx, dep.ID, dep.Kind, 1, "")
					if err != nil {
						t.Fatal(err)
					}
					claim, err := s.ClaimQueuedBuild(ctx, b.ID)
					if err != nil {
						t.Fatal(err)
					}
					prov := state.BuildProvenance{BuildID: b.ID, SourceSHA256: "abc", Plan: string(api.PlanPro), BuilderNodeID: "default-local", StartedAt: claim.StartedAt, FinishedAt: time.Now()}
					switch scenario {
					case "cancelled":
						err = s.UpdateBuildStatus(ctx, b.ID, state.BuildCancelled, "", false, true)
					case "reaped":
						err = s.UpdateBuildStatus(ctx, b.ID, state.BuildFailed, state.FailureTimeout, false, true)
					case "new-claim":
						claim.StartedAt = claim.StartedAt.Add(-time.Second)
					case "deployment-cancelled":
						err = s.UpdateDeploymentStatus(ctx, dep.ID, state.DeployCancelled, "")
					}
					if err != nil {
						t.Fatal(err)
					}
					if scenario == "failure" {
						if err = s.FailBuild(ctx, claim, state.FailureInfra, "interrupted"); err != nil {
							t.Fatal(err)
						}
						got, _ := s.DeploymentByID(ctx, dep.ID)
						if got.Status != state.DeployFailed {
							t.Fatalf("deployment status %s", got.Status)
						}
						err = s.CompleteBuild(ctx, claim, "/image.tar", "app/key", 42, prov)
					} else {
						err = s.CompleteBuild(ctx, claim, "/image.tar", "app/key", 42, prov)
					}
					if scenario != "success" {
						if !errors.Is(err, state.ErrNotFound) {
							t.Fatalf("stale completion: %v", err)
						}
						if err = s.FailBuild(ctx, claim, state.FailureInfra, "late failure"); !errors.Is(err, state.ErrNotFound) {
							t.Fatalf("stale failure: %v", err)
						}
						got, _ := s.DeploymentByID(ctx, dep.ID)
						if got.RootfsPath != "" {
							t.Fatal("stale artifact published")
						}
						if _, err = s.BuildProvenanceByBuildID(ctx, b.ID); !errors.Is(err, state.ErrNotFound) {
							t.Fatalf("stale provenance: %v", err)
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					got, _ := s.BuildByID(ctx, b.ID)
					if got.Status != state.BuildSucceeded {
						t.Fatalf("status: %s", got.Status)
					}
					if _, err = s.BuildProvenanceByBuildID(ctx, b.ID); err != nil {
						t.Fatal(err)
					}
					for _, node := range []string{"default-local", ""} {
						work, err := s.ListBuildsAwaitingImage(ctx, node, 16)
						if err != nil || len(work) != 1 || work[0].DeploymentID != dep.ID {
							t.Fatalf("recovery %q: %+v %v", node, work, err)
						}
					}
					work, err := s.ListBuildsAwaitingImage(ctx, "other-node", 16)
					if err != nil || len(work) != 0 {
						t.Fatalf("foreign recovery: %+v %v", work, err)
					}
					prov.SBOMStorageKey = "sbom/key"
					if err = s.CreateBuildProvenance(ctx, prov); err != nil {
						t.Fatal(err)
					}
					prov.SBOMStorageKey = ""
					if err = s.CreateBuildProvenance(ctx, prov); err != nil {
						t.Fatal(err)
					}
					stored, err := s.BuildProvenanceByBuildID(ctx, b.ID)
					if err != nil || stored.SBOMStorageKey != "sbom/key" {
						t.Fatalf("lost SBOM: %+v %v", stored, err)
					}
					if err = s.UpdateDeploymentStatus(ctx, dep.ID, state.DeployFailed, "end fixture"); err != nil {
						t.Fatal(err)
					}
				})
			}
		})
	}
}

func TestPgCompletionRollsBackWhenProvenanceFails(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	_, appID, _ := seedLiveDeploy(t, s, ctx)
	dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: appID, Kind: state.DeploymentKindTarball, Status: state.DeployBuilding})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateBuild(ctx, dep.ID, dep.Kind, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimQueuedBuild(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Fail after both earlier writes, inside the real completion transaction.
	_, err = pool.Exec(ctx, `create function reject_provenance() returns trigger language plpgsql as $$begin raise exception 'injected provenance failure';end$$;
 create trigger reject_provenance before insert on build_provenance for each row execute function reject_provenance()`)
	if err != nil {
		t.Fatal(err)
	}
	err = s.CompleteBuild(ctx, claim, "/image.tar", "key", 42, state.BuildProvenance{BuildID: b.ID, SourceSHA256: "abc", Plan: "pro"})
	if err == nil {
		t.Fatal("expected provenance failure")
	}
	got, err := s.BuildByID(ctx, b.ID)
	if err != nil || got.Status != state.BuildRunning {
		t.Fatalf("partial success: %+v %v", got, err)
	}
	d, err := s.DeploymentByID(ctx, dep.ID)
	if err != nil || d.RootfsPath != "" {
		t.Fatalf("partial artifact: %+v %v", d, err)
	}
}

func TestSourceQueueContract(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			var s state.Store = state.NewMemStore()
			if backend == "postgres" {
				s, ctx = pgStore(t)
			}
			acct, err := s.CreateAccount(ctx, "queue@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "queue-app", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
			if err != nil {
				t.Fatal(err)
			}
			for i, status := range []state.DeploymentStatus{state.DeployPending, state.DeployCancelled} {
				dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployPending})
				if err != nil {
					t.Fatal(err)
				}
				if err = s.UpdateDeploymentStatus(ctx, dep.ID, status, ""); err != nil {
					t.Fatal(err)
				}
				id := []string{"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"}[i]
				build, err := s.CreateBuildWithID(ctx, id, dep.ID, dep.Kind, 42, "")
				if status == state.DeployCancelled {
					if !errors.Is(err, state.ErrNotFound) {
						t.Fatalf("cancelled enqueue: %v", err)
					}
					if err = s.FailSourceDeployment(ctx, dep.ID, "late enqueue failure"); err != nil {
						t.Fatal(err)
					}
					got, _ := s.DeploymentByID(ctx, dep.ID)
					if got.Status != status {
						t.Fatalf("cancel overwritten: %s", got.Status)
					}
					continue
				}
				if err != nil || build.ID != id {
					t.Fatalf("queued identity: %+v %v", build, err)
				}
				if err = s.FailSourceDeployment(ctx, dep.ID, "uncertain commit response"); err != nil {
					t.Fatal(err)
				}
				got, _ := s.DeploymentByID(ctx, dep.ID)
				if got.Status != state.DeployBuilding {
					t.Fatalf("queued deployment status: %s", got.Status)
				}
			}
		})
	}
}
