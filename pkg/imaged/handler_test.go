package imaged

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeOCI lets the handler run end-to-end without a registry call.
type fakeOCI struct{ digest string }

func (f fakeOCI) PullDigest(_ context.Context, _ string) (string, error) { return f.digest, nil }

// failingOCI surfaces a pull error to drive the failed path.
type failingOCI struct{ err error }

func (f failingOCI) PullDigest(_ context.Context, _ string) (string, error) { return "", f.err }

// fakeNotifier records every Notify so tests can assert fan-out.
type fakeNotifier struct {
	calls []notifyCall
}

type notifyCall struct{ channel, payload string }

func (f *fakeNotifier) Notify(_ context.Context, channel, payload string) error {
	f.calls = append(f.calls, notifyCall{channel, payload})
	return nil
}

// TestHandleDeploymentHappyPath walks an image-kind deployment through every
// transition and asserts the final state plus the snapshot row.
func TestHandleDeploymentHappyPath(t *testing.T) {
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(context.Background(), "u@example.com", "pro")
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "img-app", RAMMB: 512, IdleTimeoutS: 60, MaxConcurrency: 5,
	})
	dep, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, ImageDigest: "sha256:abc", Kind: state.DeploymentKindImage,
	})
	notif := &fakeNotifier{}
	h := New(store, notif, fakeOCI{digest: "sha256:abc"}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	n := db.Notification{
		Channel: db.NotifyDeploymentChanged,
		Payload: `{"app_id":"` + app.ID + `","to":"` + dep.ID + `","kind":"image","image_digest":"sha256:abc"}`,
	}
	h.HandleNotification(context.Background(), n)

	got, err := store.DeploymentByID(context.Background(), dep.ID)
	if err != nil {
		t.Fatalf("DeploymentByID: %v", err)
	}
	if got.Status != state.DeployLive {
		t.Errorf("status = %s, want live", got.Status)
	}
	snap, err := store.LatestSnapshot(context.Background(), dep.ID)
	if err != nil {
		t.Fatalf("LatestSnapshot: %v", err)
	}
	if snap.DeploymentID != dep.ID || snap.FCVersion == "" {
		t.Errorf("snapshot row wrong: %+v", snap)
	}
	if snap.MemBytes != int64(512)*1024*1024 {
		t.Errorf("MemBytes = %d, want %d", snap.MemBytes, int64(512)*1024*1024)
	}
	if len(notif.calls) == 0 {
		t.Error("expected at least one notification fan-out")
	}
}

// TestHandleDeploymentTarballIgnored verifies non-image kinds return nil (they
// live on the build_queued stream).
func TestHandleDeploymentTarballIgnored(t *testing.T) {
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(context.Background(), "u@example.com", "pro")
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "tar-app", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
	})
	dep, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindTarball, SourcePath: "/tmp/x.tgz",
	})
	h := New(store, &fakeNotifier{}, fakeOCI{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	n := db.Notification{
		Channel: db.NotifyDeploymentChanged,
		Payload: `{"app_id":"` + app.ID + `","to":"` + dep.ID + `","kind":"tarball"}`,
	}
	h.HandleNotification(context.Background(), n)
	got, _ := store.DeploymentByID(context.Background(), dep.ID)
	if got.Status != state.DeployPending {
		t.Errorf("tarball deploy should stay pending, got %s", got.Status)
	}
}

// TestHandleDeploymentOCIFailure marks the deployment failed and surfaces the
// error to the caller (logged, not returned — the loop swallows).
func TestHandleDeploymentOCIFailure(t *testing.T) {
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(context.Background(), "u@example.com", "pro")
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "bad-img", RAMMB: 128, IdleTimeoutS: 30, MaxConcurrency: 1,
	})
	dep, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, ImageDigest: "sha256:bad", Kind: state.DeploymentKindImage,
	})
	h := New(store, &fakeNotifier{}, failingOCI{err: errors.New("net down")},
		nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	n := db.Notification{
		Channel: db.NotifyDeploymentChanged,
		Payload: `{"app_id":"` + app.ID + `","to":"` + dep.ID + `","kind":"image","image_digest":"sha256:bad"}`,
	}
	h.HandleNotification(context.Background(), n)
	got, _ := store.DeploymentByID(context.Background(), dep.ID)
	if got.Status != state.DeployFailed {
		t.Errorf("status = %s, want failed", got.Status)
	}
	if got.Error == "" {
		t.Error("error message should be populated")
	}
}

// TestHandleBuildQueued asserts the queued build is marked running.
func TestHandleBuildQueued(t *testing.T) {
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(context.Background(), "u@example.com", "pro")
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "src-app", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
	})
	dep, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindTarball, SourcePath: "/tmp/x.tgz",
	})
	b, err := store.CreateBuild(context.Background(), dep.ID, state.DeploymentKindTarball, 4096, "/var/log/x.log")
	if err != nil {
		t.Fatal(err)
	}
	h := New(store, &fakeNotifier{}, fakeOCI{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	n := db.Notification{
		Channel: db.NotifyBuildQueued,
		Payload: `{"app_id":"` + app.ID + `","build_id":"` + b.ID + `","kind":"tarball"}`,
	}
	h.HandleNotification(context.Background(), n)
	got, _ := store.BuildByID(context.Background(), b.ID)
	if got.Status != state.BuildRunning {
		t.Errorf("build status = %s, want running", got.Status)
	}
}
