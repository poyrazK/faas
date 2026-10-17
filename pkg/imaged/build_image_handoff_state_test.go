package imaged

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestBuildImageHandoffSnapshotStoreContract(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			if backend == "postgres" {
				store = state.NewPgStore(pgtest.OpenMigrated(t))
			}
			ctx := t.Context()
			reader := store.(state.BuildImageHandoffObservationStore)
			empty, err := reader.BuildImageHandoffSnapshot(ctx, "node-a")
			if err != nil || empty.Pending != 0 || !empty.OldestFinishedAt.IsZero() {
				t.Fatalf("empty snapshot=%+v err=%v", empty, err)
			}
			acct, err := store.CreateAccount(ctx, "handoff-observation@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "handoff-observation", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
			if err != nil {
				t.Fatal(err)
			}
			queuedDep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball})
			if err != nil {
				t.Fatal(err)
			}
			queuedBuild, err := store.CreateBuild(ctx, queuedDep.ID, queuedDep.Kind, 1, "")
			if err != nil {
				t.Fatal(err)
			}
			if err := store.SetDeploymentRootfs(ctx, queuedDep.ID, "/queued/image.tar", "builder/queued", 42); err != nil {
				t.Fatal(err)
			}
			if err := store.CreateBuildProvenance(ctx, state.BuildProvenance{BuildID: queuedBuild.ID, BuilderNodeID: "node-a"}); err != nil {
				t.Fatal(err)
			}
			finished := map[string]time.Time{}
			var own []state.Deployment
			for i := range 20 {
				// A new deployment supersedes older candidates of the same app
				// in PostgreSQL. Independent apps model a real multi-build backlog.
				candidate, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: fmt.Sprintf("handoff-observation-%d", i), Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
				if err != nil {
					t.Fatal(err)
				}
				node := "node-a"
				if i >= 17 {
					node = "node-b"
				}
				if i == 19 {
					node = ""
				}
				dep, build := seedObservedHandoff(t, store, candidate.ID, node)
				finished[dep.ID] = build.FinishedAt
				if node == "node-a" {
					own = append(own, dep)
				}
			}
			for _, tc := range []struct {
				node string
				want int64
			}{{" node-a ", 18}, {"node-b", 3}, {"", 20}, {"unknown-node", 1}} {
				got, err := reader.BuildImageHandoffSnapshot(ctx, tc.node)
				if err != nil {
					t.Fatal(err)
				}
				if got.Pending != tc.want {
					t.Fatalf("scope %q count=%d want=%d", tc.node, got.Pending, tc.want)
				}
				// Use the real recovery predicate as a separate oracle, above its
				// normal 16-item limit, and derive the expected oldest finish.
				work, err := store.ListBuildsAwaitingImage(ctx, strings.TrimSpace(tc.node), 100)
				if err != nil {
					t.Fatal(err)
				}
				var oldest time.Time
				for _, item := range work {
					at := finished[item.DeploymentID]
					if oldest.IsZero() || at.Before(oldest) {
						oldest = at
					}
				}
				if int64(len(work)) != got.Pending || !oldest.Equal(got.OldestFinishedAt) {
					t.Fatalf("scope %q snapshot=%+v recovery=%d oldest=%v", tc.node, got, len(work), oldest)
				}
			}
			images := store.(state.DeploymentImagePreparationStore)
			p, err := images.BeginImagePreparation(ctx, own[0].ID, "node-a")
			if err != nil {
				t.Fatal(err)
			}
			assertHandoffCount(t, reader, 17)
			// A resumed claim changes UpdatedAt but does not put active work back
			// into waiting, even before its deployment status changes to imaging.
			p, err = images.BeginImagePreparation(ctx, own[0].ID, "node-a")
			if err != nil {
				t.Fatal(err)
			}
			if err := images.TransitionImagePreparation(ctx, own[0].ID, p.ClaimToken, state.DeployImaging); err != nil {
				t.Fatal(err)
			}
			if err := images.PublishImagePreparationLayer(ctx, own[0].ID, p.ClaimToken, "/final.ext4", "apps/final.ext4", 42); err != nil {
				t.Fatal(err)
			}
			assertHandoffCount(t, reader, 17)
			if err := store.UpdateDeploymentStatus(ctx, own[1].ID, state.DeployCancelled, ""); err != nil {
				t.Fatal(err)
			}
			assertHandoffCount(t, reader, 16)
			if err := store.UpdateDeploymentStatus(ctx, own[2].ID, state.DeployFailed, "test failure"); err != nil {
				t.Fatal(err)
			}
			assertHandoffCount(t, reader, 15)
			if err := store.SetDeploymentRootfs(ctx, own[3].ID, "", "builder/missing", 0); err != nil {
				t.Fatal(err)
			}
			assertHandoffCount(t, reader, 14)
			cctx, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := reader.BuildImageHandoffSnapshot(cctx, "node-a"); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled read: %v", err)
			}
			if _, err := store.ClaimQueuedBuild(ctx, queuedBuild.ID); err != nil {
				t.Fatal(err)
			}
			if err := store.UpdateBuildStatus(ctx, queuedBuild.ID, state.BuildSucceeded, "", false, false); err != nil {
				t.Fatal(err)
			}
			if _, err := reader.BuildImageHandoffSnapshot(ctx, "node-a"); err == nil {
				t.Fatal("succeeded build without finish time became known backlog")
			}
		})
	}
}

func seedObservedHandoff(t *testing.T, store state.Store, appID, node string) (state.Deployment, state.Build) {
	t.Helper()
	ctx := t.Context()
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: appID, Kind: state.DeploymentKindTarball, Status: state.DeployBuilding})
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.CreateBuild(ctx, dep.ID, dep.Kind, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimQueuedBuild(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteBuild(ctx, claim, "/handoff/image.tar", "builder/"+b.ID, 42, state.BuildProvenance{BuildID: b.ID, BuilderNodeID: node}); err != nil {
		t.Fatal(err)
	}
	b, err = store.BuildByID(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	return dep, b
}

func assertHandoffCount(t *testing.T, reader state.BuildImageHandoffObservationStore, want int64) {
	t.Helper()
	got, err := reader.BuildImageHandoffSnapshot(t.Context(), "node-a")
	if err != nil || got.Pending != want {
		t.Fatalf("snapshot=%+v err=%v want=%d", got, err, want)
	}
}

func TestBuildImageHandoffSnapshotMissingTimestampIsUnknown(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "handoff-invalid@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "handoff-invalid", RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	_, build := seedObservedHandoff(t, store, app.ID, "node-a")
	other, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "handoff-valid", RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	seedObservedHandoff(t, store, other.ID, "node-a")
	// This test owns its isolated migrated database. Corrupting the finish
	// time verifies the SQL aggregate cannot silently omit an unknown age.
	for _, stamp := range []any{nil, pgtype.Timestamptz{Valid: true, InfinityModifier: pgtype.Infinity}, pgtype.Timestamptz{Valid: true, InfinityModifier: pgtype.NegativeInfinity}} {
		if _, err := pool.Exec(ctx, "UPDATE builds SET finished_at=$2 WHERE id=$1", build.ID, stamp); err != nil {
			t.Fatal(err)
		}
		if _, err := store.BuildImageHandoffSnapshot(ctx, "node-a"); err == nil {
			t.Fatalf("accepted %v finish time", stamp)
		}
	}
}
