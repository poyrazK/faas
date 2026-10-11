package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type devPatchStore interface {
	state.Store
	state.DevSourcePatchStore
}

func TestDevSourcePatchStoreMem(t *testing.T) {
	runDevSourcePatchStoreContract(t, state.NewMemStore(), context.Background())
}

func TestDevSourcePatchStorePg(t *testing.T) {
	s, ctx := pgStore(t)
	runDevSourcePatchStoreContract(t, s, ctx)
}

func runDevSourcePatchStoreContract(t *testing.T, s devPatchStore, ctx context.Context) {
	t.Helper()
	acct, err := s.CreateAccount(ctx, "dev-patch-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "dev-patch-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	deploy := func(status state.DeploymentStatus) state.Deployment {
		t.Helper()
		dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
			ImageDigest: "sha256:" + uuid.NewString(), Status: status, CreatedAt: time.Now().UTC()})
		if err != nil {
			t.Fatal(err)
		}
		// PgStore always inserts pending; promote through the normal paths.
		if status == state.DeployLive {
			err = s.MarkDeploymentLive(ctx, dep.ID)
		} else {
			err = s.UpdateDeploymentStatus(ctx, dep.ID, status, "")
		}
		if err != nil {
			t.Fatal(err)
		}
		dep.Status = status
		return dep
	}
	live := deploy(state.DeployLive)
	if got, err := s.DeploymentByID(ctx, live.ID); err != nil || got.Status != state.DeployLive {
		t.Fatalf("seeded live deployment status = %q, %v", got.Status, err)
	}
	entries := map[string]state.DevSourceEntry{"src/app.js": {Type: '0', Mode: 0o644, Size: 3, Digest: "abc"}}

	// Manifests round-trip, and pruning keeps the live deployment's.
	if err := s.RecordDevSourceManifest(ctx, state.DevSourceManifest{DeploymentID: live.ID, AppID: app.ID, SourceRoot: "apps/api", Entries: entries}, 1); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		older := deploy(state.DeployFailed)
		if err := s.RecordDevSourceManifest(ctx, state.DevSourceManifest{DeploymentID: older.ID, AppID: app.ID, Entries: entries}, 1); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.DevSourceManifest(ctx, live.ID)
	if err != nil || got.SourceRoot != "apps/api" || got.Entries["src/app.js"].Digest != "abc" {
		t.Fatalf("live manifest = %+v, %v; want it kept by pruning", got, err)
	}

	// Patches get dense generations and only newer, unexpired ones are served.
	expires := time.Now().UTC().Add(time.Hour)
	first, err := s.CreateDevSourcePatch(ctx, state.DevSourcePatch{AppID: app.ID, BaseDeploymentID: live.ID, ImageDir: "/app",
		Archive: []byte("one"), Deleted: []string{"old.js"}, Digest: digestOf("one"), ExpiresAt: expires})
	if err != nil || first.Generation != 1 {
		t.Fatalf("first patch = %+v, %v", first, err)
	}
	second, err := s.CreateDevSourcePatch(ctx, state.DevSourcePatch{AppID: app.ID, BaseDeploymentID: live.ID, ImageDir: "/app",
		Archive: []byte("two"), Digest: digestOf("two"), ExpiresAt: expires})
	if err != nil || second.Generation != 2 {
		t.Fatalf("second patch = %+v, %v", second, err)
	}
	latest, err := s.LatestDevSourcePatch(ctx, app.ID, live.ID, 0)
	if err != nil || latest.Generation != 2 || string(latest.Archive) != "two" || latest.Deleted == nil {
		t.Fatalf("latest = %+v, %v", latest, err)
	}
	if _, err := s.LatestDevSourcePatch(ctx, app.ID, live.ID, 2); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("patch after the newest generation = %v, want ErrNotFound", err)
	}

	// The first instance acknowledgement is kept; later ones are ignored.
	if status, err := s.DevSourcePatchStatus(ctx, app.ID, 2); err != nil || status.AppliedAt != nil {
		t.Fatalf("unacknowledged status = %+v, %v", status, err)
	}
	if err := s.RecordDevSourcePatchApplied(ctx, app.ID, live.ID, 2, 640, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordDevSourcePatchApplied(ctx, app.ID, live.ID, 2, 9999, "late_instance"); err != nil {
		t.Fatal(err)
	}
	if status, err := s.DevSourcePatchStatus(ctx, app.ID, 2); err != nil || status.AppliedAt == nil || status.ApplyMS != 640 || status.ApplyError != "" {
		t.Fatalf("acknowledged status = %+v, %v; want the first acknowledgement", status, err)
	}
	if _, err := s.DevSourcePatchStatus(ctx, app.ID, 99); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("status of an unknown generation = %v, want ErrNotFound", err)
	}

	// A patch for a newer base deployment retires the old base's patches.
	next := deploy(state.DeployLive)
	if _, err := s.CreateDevSourcePatch(ctx, state.DevSourcePatch{AppID: app.ID, BaseDeploymentID: next.ID, ImageDir: "/app",
		Archive: []byte("three"), Digest: digestOf("three"), ExpiresAt: expires}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LatestDevSourcePatch(ctx, app.ID, live.ID, 0); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("old base patch after a newer base = %v, want ErrNotFound", err)
	}
}

func digestOf(value string) string {
	const hex = "0123456789abcdef"
	out := make([]byte, 64)
	for i := range out {
		out[i] = hex[(int(value[i%len(value)])+i)%16]
	}
	return string(out)
}
