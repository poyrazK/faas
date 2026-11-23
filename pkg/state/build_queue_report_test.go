package state_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// The build queue reads return the same rows on both stores
// (ADR-20261123163340): queued and running builds the longest waiting first,
// and builds finished since a time newest first, each joined to its app.
func TestBuildQueueReportStores(t *testing.T) {
	for _, backend := range []string{"mem", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			var s interface {
				state.BuildQueueReportReader
				CreateAccount(context.Context, string, api.Plan) (state.Account, error)
				CreateApp(context.Context, state.App) (state.App, error)
				CreateDeployment(context.Context, state.Deployment) (state.Deployment, error)
				CreateBuild(context.Context, string, state.DeploymentKind, int64, string) (state.Build, error)
				UpdateBuildStatus(context.Context, string, state.BuildStatus, state.FailureClass, bool, bool) error
				RecordBuildNode(context.Context, string, string) error
			} = state.NewMemStore()
			// Moves a build through running, where builderd records its
			// node, to its status.
			advance := func(b state.Build, status state.BuildStatus, fc state.FailureClass) {
				t.Helper()
				if err := s.UpdateBuildStatus(ctx, b.ID, state.BuildRunning, "", true, false); err != nil {
					t.Fatal(err)
				}
				if err := s.RecordBuildNode(ctx, b.ID, "fsn-2"); err != nil {
					t.Fatal(err)
				}
				if status != state.BuildRunning {
					if err := s.UpdateBuildStatus(ctx, b.ID, status, fc, false, true); err != nil {
						t.Fatal(err)
					}
				}
			}
			if backend == "postgres" {
				pool := pgtest.OpenMigrated(t)
				s = state.NewPgStore(pool)
				memAdvance := advance
				advance = func(b state.Build, status state.BuildStatus, fc state.FailureClass) {
					t.Helper()
					memAdvance(b, status, fc)
					if _, err := pool.Exec(ctx, `UPDATE builds SET preemptions = 1 WHERE id = $1`, b.ID); err != nil {
						t.Fatal(err)
					}
				}
			}
			acct, err := s.CreateAccount(ctx, "build-queue@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			build := func(slug string) state.Build {
				t.Helper()
				a, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug, Status: state.AppActive})
				if err != nil {
					t.Fatal(err)
				}
				d, err := s.CreateDeployment(ctx, state.Deployment{ID: uuid.NewString(), AppID: a.ID, Kind: state.DeploymentKindImage,
					Status: state.DeployPending, ImageDigest: "sha256:" + fmt.Sprintf("%064d", 1)})
				if err != nil {
					t.Fatal(err)
				}
				b, err := s.CreateBuild(ctx, d.ID, state.DeploymentKindTarball, 2048, "")
				if err != nil {
					t.Fatal(err)
				}
				return b
			}
			waiting := build("bq-waiting")
			running := build("bq-running")
			advance(running, state.BuildRunning, "")
			ok := build("bq-ok")
			advance(ok, state.BuildSucceeded, "")
			oom := build("bq-oom")
			advance(oom, state.BuildFailed, state.FailureOOM)

			act, err := s.OperatorActiveBuilds(ctx, 10)
			if err != nil {
				t.Fatal(err)
			}
			// Running builds come first even though this one was enqueued
			// later: a long backlog never cuts them.
			if len(act) != 2 || act[1].ID != waiting.ID || act[1].Status != "queued" || act[1].AppSlug != "bq-waiting" || act[1].AccountID != acct.ID || act[1].NodeName != "" {
				t.Fatalf("active = %+v", act)
			}
			if act[0].ID != running.ID || act[0].StartedAt.IsZero() || act[0].Kind != "tarball" || act[0].SourceBytes != 2048 || act[0].NodeName != "fsn-2" {
				t.Fatalf("running = %+v", act[0])
			}
			if backend == "postgres" && act[0].Preemptions != 1 {
				t.Fatalf("running preemptions = %+v", act[0])
			}
			done, err := s.OperatorFinishedBuildsSince(ctx, time.Now().Add(-time.Hour), 10)
			if err != nil {
				t.Fatal(err)
			}
			// A finished build names no node: it left running.
			if len(done) != 2 || done[0].ID != oom.ID || done[0].FailureClass != "oom" || done[0].NodeName != "" || done[1].ID != ok.ID || done[1].Status != "succeeded" {
				t.Fatalf("finished = %+v", done)
			}
			if done, err := s.OperatorFinishedBuildsSince(ctx, time.Now().Add(time.Hour), 10); err != nil || len(done) != 0 {
				t.Fatalf("finished after now = %+v err=%v", done, err)
			}
			if act, err := s.OperatorActiveBuilds(ctx, 1); err != nil || len(act) != 1 {
				t.Fatalf("limited active = %+v err=%v", act, err)
			}
			for _, call := range []func() error{
				func() error { _, err := s.OperatorActiveBuilds(ctx, 0); return err },
				func() error { _, err := s.OperatorFinishedBuildsSince(ctx, time.Now(), 0); return err },
			} {
				if call() == nil {
					t.Fatal("limit 0: want error")
				}
			}
		})
	}
}
