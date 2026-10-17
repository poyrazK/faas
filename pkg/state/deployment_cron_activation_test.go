package state_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestDeploymentCronActivationMemory(t *testing.T) {
	testCronActivation(t, func(t *testing.T) state.Store { return state.NewMemStore() })
}
func testCronActivation(t *testing.T, newStore func(*testing.T) state.Store) {
	for _, mode := range []string{"ready", "failed", "cancelled", "user-edit", "dark", "canary", "superseded", "quota", "user-aba", "canary-complete", "later-dark", "upload", "scope", "competing", "no-op-quota"} {
		t.Run(mode, func(t *testing.T) {
			store := newStore(t)
			app, p, _ := cronStageFixture(t, store)
			ctx := t.Context()
			staging := store.(state.DeploymentCronStagingStore)
			worker := store.(state.DeploymentCronActivationStore)
			p.Request.Operation = "prepare"
			if mode == "no-op-quota" {
				p.Request.Desired = p.Request.Before
			}
			receipt, err := staging.ApplyDeploymentCronStaging(ctx, app.ID, p)
			if err != nil || receipt.Changed || receipt.Receipt.Status != "prepared" {
				t.Fatalf("prepare=%+v err=%v", receipt, err)
			}
			duplicate, err := staging.ApplyDeploymentCronStaging(ctx, app.ID, p)
			if err != nil || duplicate != receipt {
				t.Fatalf("duplicate=%+v err=%v", duplicate, err)
			}
			assertBefore := func() {
				t.Helper()
				snapshot, err := staging.DeploymentCronStagingSnapshot(ctx, p.AccountID, app.ID)
				if err != nil || !api.DeploymentCronsEqual(snapshot.Crons, p.Request.Before) {
					t.Fatalf("active crons changed before readiness: %+v err=%v", snapshot, err)
				}
			}
			assertBefore()
			if changes, err := worker.ReconcileDeploymentCrons(ctx); err != nil || len(changes) != 0 {
				t.Fatalf("unsubmitted=%v err=%v", changes, err)
			}
			candidate := state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployPending, TrafficPercent: 100}
			if mode == "scope" {
				candidate.Scope = "preview-a"
			}
			if mode == "competing" {
				predecessor, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, TrafficPercent: 25, TrafficPercentExplicit: true})
				if err != nil {
					t.Fatal(err)
				}
				if err = store.UpdateDeploymentStatus(ctx, predecessor.ID, state.DeployLive, ""); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "dark" {
				candidate.TrafficPercent = 0
				candidate.TrafficPercentExplicit = true
			}
			if mode == "canary-complete" {
				candidate.CanaryTotalSteps = 2
				candidate.RolloutState = "complete"
			}
			if mode == "canary" {
				candidate.TrafficPercent = 50
				candidate.CanaryTotalSteps = 2
				candidate.RolloutState = "rolling_out"
			}
			deployCtx := state.ContextWithDeploymentSubmission(ctx, p.Request.SessionID)
			uploadID := ""
			if mode == "upload" {
				uploadID = "opaque_upload-proof123"
				var accountUUID pgtype.UUID
				if err := accountUUID.Scan(p.AccountID); err != nil {
					t.Fatal(err)
				}
				if _, err := store.CreateUploadSession(ctx, sqlc.CreateUploadSessionParams{ID: uploadID, AccountID: accountUUID, AppSlug: app.Slug, TotalSize: 1024, ChunkSize: 512, PartPath: "/tmp/cron-activation.part", DeployOptions: []byte(`{}`)}); err != nil {
					t.Fatal(err)
				}
				track := p
				track.Request.Operation = "track_upload"
				track.Request.UploadID = uploadID
				if _, err := staging.ApplyDeploymentCronStaging(ctx, app.ID, track); err != nil {
					t.Fatal(err)
				}
				deployCtx = ctx // A cached upload has no receipt for this new saved attempt.
			}
			dep, err := store.CreateDeployment(deployCtx, candidate)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "upload" {
				if _, err = store.MarkUploadSessionCommitted(ctx, sqlc.MarkUploadSessionCommittedParams{ID: uploadID, DeploymentID: pgtype.Text{String: dep.ID, Valid: true}}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = worker.ReconcileDeploymentCrons(ctx); err != nil {
				t.Fatal(err)
			}
			assertBefore()
			canceled := p
			canceled.Request.Operation = "cancel"
			if _, err = staging.ApplyDeploymentCronStaging(ctx, app.ID, canceled); !errors.Is(err, state.ErrDeploymentCronConflict) {
				t.Fatalf("accepted cancel=%v", err)
			}
			status := state.DeployLive
			if mode == "failed" {
				status = state.DeployFailed
			}
			if mode == "cancelled" {
				status = state.DeployCancelled
			}
			if err = store.UpdateDeploymentStatus(ctx, dep.ID, status, ""); err != nil {
				t.Fatal(err)
			}
			if mode == "user-edit" {
				path := "/operator"
				if _, err = store.UpdateCron(ctx, p.Request.Before[0].ID, nil, &path, nil, nil); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "quota" || mode == "no-op-quota" {
				if err = store.UpdateAccountPlan(ctx, p.AccountID, api.PlanFree); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "user-aba" {
				path := "/temporary"
				if _, err = store.UpdateCron(ctx, p.Request.Before[0].ID, nil, &path, nil, nil); err != nil {
					t.Fatal(err)
				}
				path = p.Request.Before[0].Path
				if _, err = store.UpdateCron(ctx, p.Request.Before[0].ID, nil, &path, nil, nil); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "later-dark" {
				if _, err = store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployPending, TrafficPercent: 0, TrafficPercentExplicit: true}); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "superseded" {
				if _, err = store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployPending, TrafficPercent: 100}); err != nil {
					t.Fatal(err)
				}
			}
			var mu sync.Mutex
			activated := 0
			var wg sync.WaitGroup
			for range 4 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					changes, err := worker.ReconcileDeploymentCrons(ctx)
					mu.Lock()
					defer mu.Unlock()
					if err != nil {
						t.Errorf("worker=%v", err)
					}
					activated += len(changes)
				}()
			}
			wg.Wait()
			result, err := staging.ApplyDeploymentCronStaging(ctx, app.ID, p)
			if err != nil {
				t.Fatal(err)
			}
			want := "activated"
			switch mode {
			case "failed", "cancelled", "dark", "superseded", "scope":
				want = "discarded"
			case "user-edit", "quota", "user-aba", "no-op-quota":
				want = "conflict"
			case "canary", "competing":
				want = "prepared"
			}
			if result.Receipt.Status != want {
				t.Fatalf("status=%s want=%s", result.Receipt.Status, want)
			}
			if want == "activated" {
				if activated != 1 {
					t.Fatalf("activations=%d", activated)
				}
				snapshot, err := staging.DeploymentCronStagingSnapshot(ctx, p.AccountID, app.ID)
				if err != nil || !api.DeploymentCronsEqual(snapshot.Crons, p.Request.Desired) {
					t.Fatalf("after=%+v err=%v", snapshot, err)
				}
			} else {
				if activated != 0 {
					t.Fatalf("unexpected activation=%d", activated)
				}
				if mode != "user-edit" {
					assertBefore()
				}
			}
		})
	}
}

func TestDeploymentCronActivationCancelFence(t *testing.T) {
	store := state.NewMemStore()
	app, p, _ := cronStageFixture(t, store)
	p.Request.Operation = "cancel"
	if _, err := store.ApplyDeploymentCronStaging(t.Context(), app.ID, p); err != nil {
		t.Fatal(err)
	}
	p.Request.Operation = "prepare"
	if _, err := store.ApplyDeploymentCronStaging(t.Context(), app.ID, p); !errors.Is(err, state.ErrDeploymentCronConflict) {
		t.Fatalf("late prepare=%v", err)
	}
	snapshot, err := store.DeploymentCronStagingSnapshot(t.Context(), p.AccountID, app.ID)
	if err != nil || !api.DeploymentCronsEqual(snapshot.Crons, p.Request.Before) {
		t.Fatalf("cancel changed crons: %+v err=%v", snapshot, err)
	}
}
