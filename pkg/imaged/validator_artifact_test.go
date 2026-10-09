// adr: 857
package imaged

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestValidatorArtifactRefusalPreventsInitialLivePublication(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "validator-publication@example.com", "pro")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "validator-publication", RAMMB: 512, IdleTimeoutS: 60, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:abc", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.UpdateDeploymentStatus(ctx, dep.ID, state.DeploySnapshotting, ""); err != nil {
		t.Fatal(err)
	}
	called := false
	h := New(store, &fakeNotifier{}, fakePuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger()).WithValidatorArtifactCheck(func(_ context.Context, appID, deploymentID string) error {
		called = true
		if appID != app.ID || deploymentID != dep.ID {
			t.Fatal("wrong validator identity")
		}
		return errors.New("validator unavailable")
	})
	h.HandleNotification(ctx, db.Notification{Channel: db.NotifySnapshotWritten, Payload: `{"deployment_id":"` + dep.ID + `","vmstate_path":"/srv/fc/snap/` + dep.ID + `/vmstate","storage_key":"snap/` + dep.ID + `/mem","mem_bytes":536870912,"vmstate_bytes":40960,"fc_version":"firecracker-1.10","base_image_version":"v1"}`})
	got, err := store.DeploymentByID(ctx, dep.ID)
	if err != nil || !called || got.Status == state.DeployLive {
		t.Fatal("candidate published without validator", called, got.Status, err)
	}
}
