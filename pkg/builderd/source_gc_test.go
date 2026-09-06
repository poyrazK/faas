package builderd

import (
	"context"
	"encoding/binary"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

func TestSourceGCSweepOnce_ProtectsLiveBuildsAndRemovesExpiredObjects(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	backend, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStorageBackend: %v", err)
	}
	store := state.NewMemStore()

	orphanOld := sourceIDAt(now.Add(-48 * time.Hour))
	orphanFresh := sourceIDAt(now.Add(-2 * time.Hour))
	queuedOld := sourceIDAt(now.Add(-48 * time.Hour))
	terminalOld := sourceIDAt(now.Add(-48 * time.Hour))
	legacy := uuid.NewString()
	for _, id := range []string{orphanOld, orphanFresh, queuedOld, terminalOld, legacy} {
		if err := backend.Put(ctx, sourceKey(id), strings.NewReader(id)); err != nil {
			t.Fatalf("Put %s: %v", id, err)
		}
	}
	seedSourceBuild(t, store, queuedOld, false)
	seedSourceBuild(t, store, terminalOld, true)

	deleted, err := SourceGCSweepOnce(ctx, backend, store, 24*time.Hour, now, quietLogger())
	if err != nil {
		t.Fatalf("SourceGCSweepOnce: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("deleted = %d, want 2", deleted)
	}
	for _, id := range []string{orphanOld, terminalOld} {
		if _, err := backend.Get(ctx, sourceKey(id)); !storage.IsNotFound(err) {
			t.Errorf("expired source %s still exists: err=%v", id, err)
		}
	}
	for _, id := range []string{orphanFresh, queuedOld, legacy} {
		if rc, err := backend.Get(ctx, sourceKey(id)); err != nil {
			t.Errorf("protected source %s missing: %v", id, err)
		} else {
			_ = rc.Close()
		}
	}
}

func TestSourceGCSweepOnce_UnknownBackendCapabilityFailsClosed(t *testing.T) {
	store := state.NewMemStore()
	_, err := SourceGCSweepOnce(context.Background(), noListBackend{}, store, time.Hour, time.Now(), quietLogger())
	if err == nil || !strings.Contains(err.Error(), "does not support List") {
		t.Fatalf("error = %v, want missing List capability", err)
	}
}

func sourceKey(id string) string { return sourceObjectPrefix + id + ".tar.gz" }

func sourceIDAt(t time.Time) string {
	u, err := uuid.NewV7()
	if err != nil {
		panic(err)
	}
	ms := uint64(t.UnixMilli())
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], ms<<16)
	copy(u[:8], encoded[:])
	u[6] = (u[6] & 0x0f) | 0x70
	u[8] = (u[8] & 0x3f) | 0x80
	return u.String()
}

func seedSourceBuild(t *testing.T, store state.Store, buildID string, terminal bool) {
	t.Helper()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, buildID+"@example.com", api.Plan("pro"))
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "source-" + buildID[27:35], RAMMB: 256, IdleTimeoutS: 60, MaxConcurrency: 1})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployPending,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if _, err := store.CreateBuildWithID(ctx, buildID, dep.ID, state.DeploymentKindTarball, 1, ""); err != nil {
		t.Fatalf("CreateBuildWithID: %v", err)
	}
	if terminal {
		if _, err := store.ClaimQueuedBuild(ctx, buildID); err != nil {
			t.Fatalf("ClaimQueuedBuild: %v", err)
		}
		if err := store.UpdateBuildStatus(ctx, buildID, state.BuildSucceeded, "", false, true); err != nil {
			t.Fatalf("UpdateBuildStatus: %v", err)
		}
	}
}

type noListBackend struct{}

func (noListBackend) Put(context.Context, string, io.Reader) error { return nil }
func (noListBackend) Get(context.Context, string) (io.ReadCloser, error) {
	return nil, storage.ErrNotFound
}
func (noListBackend) Delete(context.Context, string) error { return nil }

var _ storage.StorageBackend = noListBackend{}
