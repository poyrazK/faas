// adr: 911
package imaged

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// A plain rollback (manual or first-wake 5xx auto-rollback) prepares an older
// image or GitHub revision. The safe-release drill found the latest-revision
// fence superseding every such target, so the rollback never served. A target
// prepared by PrepareDeploymentRollback must be promoted unfenced and replace
// the newer release.
func TestPlainRollbackPromotesOlderImageAndGitHubRevisions(t *testing.T) {
	for _, kind := range []state.DeploymentKind{state.DeploymentKindGitHub, state.DeploymentKindImage} {
		t.Run(string(kind), func(t *testing.T) {
			store := state.NewMemStore()
			ctx := t.Context()
			acct, err := store.CreateAccount(ctx, "plain-rollback@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "plain-rollback", RAMMB: 256, MaxConcurrency: 5})
			if err != nil {
				t.Fatal(err)
			}
			create := func(commit string) state.Deployment {
				d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: kind, ImageDigest: "sha256:" + strings.Repeat("1", 64), CommitSHA: strings.Repeat(commit, 40), GitHubSourceRef: "main", Status: state.DeployPending})
				if err != nil {
					t.Fatal(err)
				}
				if err = store.SetDeploymentRootfs(ctx, d.ID, "/test/"+d.ID, "test/"+d.ID, 4096); err != nil {
					t.Fatal(err)
				}
				if err = store.MarkDeploymentLive(ctx, d.ID); err != nil {
					t.Fatal(err)
				}
				return d
			}
			target, current := create("a"), create("b")
			if _, err := store.PrepareDeploymentRollback(ctx, app.ID, target.ID); err != nil {
				t.Fatalf("PrepareDeploymentRollback: %v", err)
			}
			h := New(store, &fakeNotifier{}, fakePuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger()).WithHostingSmoke(func(context.Context, state.App, state.Deployment) (apihostingreceipt.SmokeResult, error) {
				return apihostingreceipt.SmokeResult{Status: apihostingreceipt.SmokeVerified, StatusCode: http.StatusOK, Path: "/healthz"}, nil
			})
			h.HandleNotification(ctx, db.Notification{Channel: db.NotifySnapshotWritten, Payload: `{"deployment_id":"` + target.ID + `","vmstate_path":"/tmp/rollback/vmstate","storage_key":"snap/` + target.ID + `/mem","mem_bytes":268435456,"vmstate_bytes":40960,"fc_version":"firecracker-1.10"}`})

			rolledBack, _ := store.DeploymentByID(ctx, target.ID)
			replaced, _ := store.DeploymentByID(ctx, current.ID)
			if rolledBack.Status != state.DeployLive || rolledBack.TrafficPercent != 100 {
				t.Fatalf("rollback target = status:%s traffic:%d error:%q, want live at 100%%", rolledBack.Status, rolledBack.TrafficPercent, rolledBack.Error)
			}
			if replaced.Status != state.DeploySuperseded || replaced.TrafficPercent != 0 {
				t.Fatalf("replaced release = status:%s traffic:%d, want superseded", replaced.Status, replaced.TrafficPercent)
			}
			if prepared, err := store.DeploymentRollbackPrepared(ctx, target.ID); err != nil || prepared {
				t.Fatalf("rollback marker after promotion = %v, %v; want consumed", prepared, err)
			}
		})
	}
}

// A CLI tarball or Dockerfile deploy used to go live in the order the builds
// finished: an older deploy that finished after a newer one had been accepted
// replaced it. imaged now fences every customer deploy kind, so the older
// candidate is superseded at its cutover and the current release keeps
// serving until the newer one is ready.
func TestOlderCLIDeployNeverReplacesNewerRevision(t *testing.T) {
	for _, kind := range []state.DeploymentKind{state.DeploymentKindTarball, state.DeploymentKindDockerfile} {
		t.Run(string(kind), func(t *testing.T) {
			store := state.NewMemStore()
			ctx := t.Context()
			acct, err := store.CreateAccount(ctx, "cli-order@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "cli-order", RAMMB: 256, MaxConcurrency: 5})
			if err != nil {
				t.Fatal(err)
			}
			create := func(digit string) state.Deployment {
				d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: kind, ImageDigest: "sha256:" + strings.Repeat(digit, 64), Status: state.DeployBuilding})
				if err != nil {
					t.Fatal(err)
				}
				if err = store.SetDeploymentRootfs(ctx, d.ID, "/test/"+d.ID, "test/"+d.ID, 4096); err != nil {
					t.Fatal(err)
				}
				if err = store.UpdateDeploymentStatus(ctx, d.ID, state.DeploySnapshotting, ""); err != nil {
					t.Fatal(err)
				}
				return d
			}
			stable := create("a")
			if err := store.MarkDeploymentLive(ctx, stable.ID); err != nil {
				t.Fatal(err)
			}
			older, newer := create("b"), create("c")
			h := New(store, &fakeNotifier{}, fakePuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger()).WithHostingSmoke(func(context.Context, state.App, state.Deployment) (apihostingreceipt.SmokeResult, error) {
				return apihostingreceipt.SmokeResult{Status: apihostingreceipt.SmokeVerified, StatusCode: http.StatusOK, Path: "/healthz"}, nil
			})
			h.HandleNotification(ctx, db.Notification{Channel: db.NotifySnapshotWritten, Payload: `{"deployment_id":"` + older.ID + `","vmstate_path":"/tmp/order/vmstate","storage_key":"snap/` + older.ID + `/mem","mem_bytes":268435456,"vmstate_bytes":40960,"fc_version":"firecracker-1.10"}`})

			if got, _ := store.DeploymentByID(ctx, older.ID); got.Status != state.DeploySuperseded {
				t.Fatalf("older %s deploy = %s at %d%% (error %q), want superseded by the newer revision", kind, got.Status, got.TrafficPercent, got.Error)
			}
			if got, _ := store.DeploymentByID(ctx, stable.ID); got.Status != state.DeployLive || got.TrafficPercent != 100 {
				t.Fatalf("serving release = %s at %d%%, want still live at 100%%", got.Status, got.TrafficPercent)
			}
			if got, _ := store.DeploymentByID(ctx, newer.ID); got.Status != state.DeploySnapshotting {
				t.Fatalf("newer deploy = %s, want still activating", got.Status)
			}
		})
	}
}
