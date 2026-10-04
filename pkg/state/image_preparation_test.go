package state_test

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestImagePreparationCheckpointContract(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			ctx := context.Background()
			if backend == "postgres" {
				store, ctx = pgStore(t)
			}
			images := store.(state.DeploymentImagePreparationStore)
			acct, err := store.CreateAccount(ctx, "image-checkpoint@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "image-checkpoint", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
			if err != nil {
				t.Fatal(err)
			}
			for _, outcome := range []string{"complete", "cancelled", "failed", "superseded", "live"} {
				t.Run(outcome, func(t *testing.T) {
					dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployBuilding})
					if err != nil {
						t.Fatal(err)
					}
					if err := store.SetDeploymentRootfs(ctx, dep.ID, "/source/image.tar", "builder/source", 11); err != nil {
						t.Fatal(err)
					}
					old, err := images.BeginImagePreparation(ctx, dep.ID, " node-a ")
					if err != nil {
						t.Fatal(err)
					}
					if _, err := images.BeginImagePreparation(ctx, dep.ID, "node-b"); !errors.Is(err, state.ErrImagePreparationNotOwned) {
						t.Fatalf("foreign begin: %v", err)
					}
					p, err := images.BeginImagePreparation(ctx, dep.ID, "node-a")
					if err != nil {
						t.Fatal(err)
					}
					if p.ClaimToken == old.ClaimToken || p.InputPath != "/source/image.tar" {
						t.Fatalf("replaced claim lost source: %+v", p)
					}
					if err := images.TransitionImagePreparation(ctx, dep.ID, old.ClaimToken, state.DeployImaging); !errors.Is(err, state.ErrConflict) {
						t.Fatalf("stale transition: %v", err)
					}
					if err := images.TransitionImagePreparation(ctx, dep.ID, p.ClaimToken, state.DeployImaging); err != nil {
						t.Fatal(err)
					}
					if err := images.PublishImagePreparationLayer(ctx, dep.ID, old.ClaimToken, "/stale.ext4", "stale", 22); !errors.Is(err, state.ErrConflict) {
						t.Fatalf("stale publication: %v", err)
					}
					if outcome != "complete" {
						terminal := state.DeploymentStatus(outcome)
						if err := store.UpdateDeploymentStatus(ctx, dep.ID, terminal, ""); err != nil {
							t.Fatal(err)
						}
						if err := images.PublishImagePreparationLayer(ctx, dep.ID, p.ClaimToken, "/late.ext4", "late", 22); !errors.Is(err, state.ErrConflict) {
							t.Fatalf("terminal publication: %v", err)
						}
						if err := images.TransitionImagePreparation(ctx, dep.ID, p.ClaimToken, state.DeployImaging); !errors.Is(err, state.ErrConflict) {
							t.Fatalf("terminal reopened: %v", err)
						}
						got, _ := store.DeploymentByID(ctx, dep.ID)
						if got.RootfsPath != "/source/image.tar" || got.Status != terminal {
							t.Fatalf("terminal mutated: %+v", got)
						}
						work, err := images.ListResumableImagePreparations(ctx, "node-a", 16)
						if err != nil || len(work) != 0 {
							t.Fatalf("terminal recoverable: %+v %v", work, err)
						}
						return
					}
					if err := images.PublishImagePreparationLayer(ctx, dep.ID, p.ClaimToken, "/final.ext4", "apps/final.ext4", 22); err != nil {
						t.Fatal(err)
					}
					resumed, err := images.BeginImagePreparation(ctx, dep.ID, "node-a")
					if err != nil || resumed.Phase != state.ImageLayerPublished || resumed.InputPath != "/source/image.tar" {
						t.Fatalf("atomic checkpoint/source: %+v %v", resumed, err)
					}
					if err := images.AdvanceImagePreparation(ctx, dep.ID, p.ClaimToken, state.ImageLayerPublished, state.ImageScanComplete); !errors.Is(err, state.ErrConflict) {
						t.Fatalf("stale scan: %v", err)
					}
					p = resumed
					if err := images.AdvanceImagePreparation(ctx, dep.ID, p.ClaimToken, state.ImageLayerPublished, state.ImageHandedOff); !errors.Is(err, state.ErrInvalidStateTransition) {
						t.Fatalf("skipped scan: %v", err)
					}
					if err := images.AdvanceImagePreparation(ctx, dep.ID, p.ClaimToken, state.ImageLayerPublished, state.ImageScanComplete); err != nil {
						t.Fatal(err)
					}
					if err := images.TransitionImagePreparation(ctx, dep.ID, p.ClaimToken, state.DeploySnapshotting); err != nil {
						t.Fatal(err)
					}
					for _, node := range []string{"node-a", "", "node-b"} {
						work, err := images.ListResumableImagePreparations(ctx, node, 16)
						want := 1
						if node == "node-b" {
							want = 0
						}
						if err != nil || len(work) != want {
							t.Fatalf("handoff recovery %q: %+v %v", node, work, err)
						}
					}
					if err := images.AdvanceImagePreparation(ctx, dep.ID, p.ClaimToken, state.ImageScanComplete, state.ImageHandedOff); err != nil {
						t.Fatal(err)
					}
					work, err := images.ListResumableImagePreparations(ctx, "node-a", 16)
					if err != nil || len(work) != 0 {
						t.Fatalf("completed recovery: %+v %v", work, err)
					}
				})
			}
		})
	}
}

func TestPgImagePublicationRollsBackCheckpointFailure(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	_, appID, _ := seedLiveDeploy(t, store, ctx)
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: appID, Kind: state.DeploymentKindImage, ImageDigest: "example.test/app:latest"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, dep.ID, "/source.tar", "builder/source", 11); err != nil {
		t.Fatal(err)
	}
	p, err := store.BeginImagePreparation(ctx, dep.ID, "node-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.TransitionImagePreparation(ctx, dep.ID, p.ClaimToken, state.DeployImaging); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_image_checkpoint() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.phase='layer_published' THEN RAISE EXCEPTION 'injected checkpoint failure'; END IF; RETURN NEW; END$$;
 CREATE TRIGGER reject_image_checkpoint BEFORE UPDATE ON deployment_image_preparations FOR EACH ROW EXECUTE FUNCTION reject_image_checkpoint()`); err != nil {
		t.Fatal(err)
	}
	if err := store.PublishImagePreparationLayer(ctx, dep.ID, p.ClaimToken, "/final.ext4", "apps/final.ext4", 22); err == nil {
		t.Fatal("expected checkpoint write failure")
	}
	got, err := store.DeploymentByID(ctx, dep.ID)
	if err != nil || got.RootfsPath != "/source.tar" || got.RootfsBytes != 11 {
		t.Fatalf("partial publication escaped rollback: %+v %v", got, err)
	}
	resumed, err := store.BeginImagePreparation(ctx, dep.ID, "node-a")
	if err != nil || resumed.Phase != state.ImagePreparing || resumed.InputPath != "/source.tar" {
		t.Fatalf("partial checkpoint escaped rollback: %+v %v", resumed, err)
	}
}
