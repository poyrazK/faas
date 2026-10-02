package apidsource

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/region"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

func seedRegionalSourceApp(t *testing.T, st *state.MemStore) state.App {
	t.Helper()
	app := mustSeedApp(t, st)
	if _, err := st.UpsertComputeRegion(t.Context(), state.ComputeRegion{ID: "eu-source", DisplayName: "Sources", AcceptingNewApps: true}); err != nil {
		t.Fatal(err)
	}
	app.ID, app.Slug, app.NodeID, app.ComputeRegion = "", "regional-source", "", "eu-source"
	app, err := st.CreateApp(t.Context(), app)
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func localSourceBackend(t *testing.T) *storage.LocalStorageBackend {
	t.Helper()
	be, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return be
}

func TestRegionalSourcePublicationAndAdmission(t *testing.T) {
	for _, scenario := range []string{"configured", "no storage", "missing route", "upload outage", "app lookup outage"} {
		t.Run(scenario, func(t *testing.T) {
			st := state.NewMemStore()
			app := seedRegionalSourceApp(t, st)
			path, size := writeHandlerOnlyTarball(t)
			regional, legacy := localSourceBackend(t), localSourceBackend(t)
			routes := map[string]storage.StorageBackend{app.ComputeRegion: regional}
			if scenario == "missing route" {
				delete(routes, app.ComputeRegion)
			}
			if scenario == "upload outage" {
				routes[app.ComputeRegion] = &memBackend{putErr: errors.New("store offline")}
			}
			router, err := storage.NewRegionRouter(routes, legacy)
			if err != nil {
				t.Fatal(err)
			}
			var be storage.StorageBackend = router
			if scenario == "no storage" {
				be = nil
			}
			var store Store = st
			if scenario == "app lookup outage" {
				store = sourceAppLookupFailure{st}
			}
			result, err := enqueueWithSourceStorage(t.Context(), store, &recordingNotifier{}, EnqueueParams{AppID: app.ID, Kind: state.DeploymentKindTarball, SourcePath: path, SourceBytes: size, FunctionRuntime: "python313", LogSpool: t.TempDir(), Log: quietLogger()}, be)
			if scenario != "configured" {
				if err == nil {
					t.Fatal("expected admission/publication error")
				}
				if _, err := st.LatestDeployment(t.Context(), app.ID); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("failed publication created durable work: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			dep, err := st.DeploymentByID(t.Context(), result.DeploymentID)
			want := region.ArtifactKey(app.ComputeRegion, "sources/"+result.BuildID+".tar.gz")
			if err != nil || dep.SourceStorageKey != want || dep.SourceSHA256 == "" {
				t.Fatalf("source identity: %+v %v", dep, err)
			}
			rc, err := router.Get(t.Context(), dep.SourceStorageKey)
			if err != nil {
				t.Fatal(err)
			}
			defer rc.Close()
			contents, err := io.ReadAll(rc)
			if err != nil || int64(len(contents)) != size {
				t.Fatalf("published archive size: %d %v", len(contents), err)
			}
			if _, err := legacy.Get(t.Context(), "sources/"+result.BuildID+".tar.gz"); !storage.IsNotFound(err) {
				t.Fatalf("regional source leaked to legacy store: %v", err)
			}
		})
	}
}

type sourceAppLookupFailure struct{ *state.MemStore }

func (sourceAppLookupFailure) AppByID(context.Context, string) (state.App, error) {
	return state.App{}, errors.New("database offline")
}

func TestRegionalRetryPreservesOriginalReferenceAndRepublishes(t *testing.T) {
	for _, historical := range []bool{false, true} {
		t.Run(map[bool]string{false: "regional reference", true: "historical legacy reference"}[historical], func(t *testing.T) {
			st := state.NewMemStore()
			app := seedRegionalSourceApp(t, st)
			path, size := writeHandlerOnlyTarball(t)
			hash, err := hashSourceFile(path)
			if err != nil {
				t.Fatal(err)
			}
			regional, legacy := localSourceBackend(t), localSourceBackend(t)
			router, err := storage.NewRegionRouter(map[string]storage.StorageBackend{app.ComputeRegion: regional}, legacy)
			if err != nil {
				t.Fatal(err)
			}
			originalID := uuid.NewString()
			originalKey := region.ArtifactKey(app.ComputeRegion, "sources/"+originalID+".tar.gz")
			storedKey := originalKey
			if historical {
				originalKey, storedKey = "sources/"+originalID+".tar.gz", ""
			}
			if err := publishSourceAtKey(t.Context(), router, originalKey, path); err != nil {
				t.Fatal(err)
			}
			dep, err := st.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, SourcePath: path, SourceBytes: size, SourceSHA256: hash, SourceStorageKey: storedKey})
			if err != nil {
				t.Fatal(err)
			}
			if historical {
				if _, err := st.CreateBuildWithID(t.Context(), originalID, dep.ID, dep.Kind, size, ""); err != nil {
					t.Fatal(err)
				}
				if err := st.MarkBuildCancelled(t.Context(), originalID, "test", false, time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			if err := st.UpdateDeploymentStatus(t.Context(), dep.ID, state.DeployFailed, ""); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			result, err := enqueueWithSourceStorage(t.Context(), st, &recordingNotifier{}, EnqueueParams{RetryOf: dep.ID, RetryFrom: state.StageSecurityScan, SourceBuildID: "untrusted-build-id", AppID: app.ID, Kind: dep.Kind, SourcePath: "/untrusted/path", SourceBytes: 999, LogSpool: t.TempDir(), Log: quietLogger()}, router)
			if err != nil {
				t.Fatal(err)
			}
			retry, err := st.DeploymentByID(t.Context(), result.DeploymentID)
			want := region.ArtifactKey(app.ComputeRegion, "sources/"+result.BuildID+".tar.gz")
			if err != nil || retry.SourceStorageKey != want || retry.SourceSHA256 != hash || retry.SourceBytes != size || retry.SourcePath != path {
				t.Fatalf("retry lost durable source inputs: %+v %v", retry, err)
			}
			rc, err := router.Get(t.Context(), want)
			if err != nil {
				t.Fatal(err)
			}
			defer rc.Close()
			bytes, err := io.ReadAll(rc)
			if err != nil || int64(len(bytes)) != size {
				t.Fatalf("retry archive: %d %v", len(bytes), err)
			}
			original, err := st.DeploymentByID(t.Context(), dep.ID)
			if err != nil || original.SourceStorageKey != storedKey {
				t.Fatalf("original reference changed: %+v %v", original, err)
			}
		})
	}
}

type retryReadOutage struct{ *storage.LocalStorageBackend }

func (retryReadOutage) Get(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("regional storage offline")
}

func TestRegionalRetryOutageDoesNotFallBackToLocalArchive(t *testing.T) {
	st := state.NewMemStore()
	app := seedRegionalSourceApp(t, st)
	path, size := writeHandlerOnlyTarball(t)
	dep, err := st.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, SourcePath: path, SourceBytes: size, SourceStorageKey: region.ArtifactKey(app.ComputeRegion, "sources/"+uuid.NewString()+".tar.gz")})
	if err != nil {
		t.Fatal(err)
	}
	router, err := storage.NewRegionRouter(map[string]storage.StorageBackend{app.ComputeRegion: retryReadOutage{localSourceBackend(t)}}, localSourceBackend(t))
	if err != nil {
		t.Fatal(err)
	}
	_, err = enqueueWithSourceStorage(t.Context(), st, &recordingNotifier{}, EnqueueParams{RetryOf: dep.ID, RetryFrom: state.StageImageBuild, AppID: app.ID, Kind: dep.Kind, SourcePath: path, SourceBytes: size, LogSpool: t.TempDir(), Log: quietLogger()}, router)
	if err == nil || !strings.Contains(err.Error(), "regional storage offline") {
		t.Fatalf("retry silently used local archive during outage: %v", err)
	}
	latest, err := st.LatestDeployment(t.Context(), app.ID)
	if err != nil || latest.ID != dep.ID {
		t.Fatalf("outage created retry: %+v %v", latest, err)
	}
}
