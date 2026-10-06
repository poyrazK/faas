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

func TestCheckedRollbackSnapshotSmokeStagesHistoricalGitWithoutCutover(t *testing.T) {
	for _, passed := range []bool{true, false} {
		t.Run(map[bool]string{true: "verified", false: "failed"}[passed], func(t *testing.T) {
			store := state.NewMemStore()
			ctx := t.Context()
			acct, err := store.CreateAccount(ctx, "rollback-image@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "rollback-image", RAMMB: 256, MaxConcurrency: 5})
			if err != nil {
				t.Fatal(err)
			}
			create := func() state.Deployment {
				d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindGitHub, ImageDigest: "sha256:" + strings.Repeat("1", 64), CommitSHA: strings.Repeat("a", 40), GitHubSourceRef: "main", Status: state.DeployPending})
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
			target, current := create(), create()
			operation, err := store.CreateCheckedRollback(ctx, acct.ID, app.ID, target.ID, current.ID, "")
			if err != nil {
				t.Fatal(err)
			}
			zero := int64(0)
			if _, err = store.SetBindingReleasePolicy(ctx, acct.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
				t.Fatal(err)
			}
			smoked := false
			h := New(store, &fakeNotifier{}, fakePuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger()).WithHostingSmoke(func(ctx context.Context, _ state.App, d state.Deployment) (apihostingreceipt.SmokeResult, error) {
				smoked = true
				old, _ := store.DeploymentByID(ctx, current.ID)
				candidate, _ := store.DeploymentByID(ctx, target.ID)
				if old.TrafficPercent != 100 || candidate.Status != state.DeploySnapshotting || candidate.TrafficPercent != 0 {
					t.Errorf("changed route before smoke: %+v %+v", old, candidate)
				}
				status := apihostingreceipt.SmokeVerified
				code := http.StatusOK
				if !passed {
					status = apihostingreceipt.SmokeFailed
					code = http.StatusServiceUnavailable
				}
				return apihostingreceipt.SmokeResult{Status: status, StatusCode: code, Path: "/healthz"}, nil
			})
			h.HandleNotification(ctx, db.Notification{Channel: db.NotifySnapshotWritten, Payload: `{"deployment_id":"` + target.ID + `","vmstate_path":"/tmp/rollback/vmstate","storage_key":"snap/` + target.ID + `/mem","mem_bytes":268435456,"vmstate_bytes":40960,"fc_version":"firecracker-1.10"}`})
			if !smoked {
				t.Fatal("checked historical rollback bypassed hosting smoke")
			}
			candidate, _ := store.DeploymentByID(ctx, target.ID)
			old, _ := store.DeploymentByID(ctx, current.ID)
			r, err := store.GetCheckedRollback(ctx, acct.ID, app.ID, operation.ID)
			if err != nil || old.TrafficPercent != 100 || candidate.TrafficPercent != 0 {
				t.Fatalf("image pipeline changed serving route: %+v %+v %v", candidate, old, err)
			}
			if passed && (r.Status != "ready" || candidate.Status != state.DeployLive) {
				t.Fatalf("historical Git target rejected by latest/branch gate: %+v %+v", r, candidate)
			}
			if !passed {
				// Previously served releases remain available for another rollback
				// attempt, while the failed smoke still prevents this cutover.
				if candidate.Status != state.DeploySuperseded || candidate.ErrorCode != api.CodeDeploymentSmokeFailed || candidate.RolloutState != "aborted" {
					t.Fatalf("failed smoke lost recovery state: status=%s code=%s rollout=%s", candidate.Status, candidate.ErrorCode, candidate.RolloutState)
				}
				if err := store.MarkDeploymentLive(ctx, target.ID); err == nil {
					t.Fatal("failed smoke target became ready for checked rollback")
				}
				old, _ = store.DeploymentByID(ctx, current.ID)
				if old.TrafficPercent != 100 {
					t.Fatal("failed historical smoke changed the serving route")
				}
			}
		})
	}
}
