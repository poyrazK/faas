package builderd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/region"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

func regionalSourceFixture(t *testing.T) (*state.MemStore, state.App, state.ComputeNode) {
	t.Helper()
	st := state.NewMemStore()
	acct, err := st.CreateAccount(t.Context(), "sources@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	id := "eu-source"
	if _, err := st.UpsertComputeRegion(t.Context(), state.ComputeRegion{ID: id, DisplayName: "Sources", AcceptingNewApps: true}); err != nil {
		t.Fatal(err)
	}
	node, err := st.CreateComputeNode(t.Context(), state.ComputeNode{Name: "source-worker", TargetURL: "unix:///tmp/source-vmmd.sock", Region: &id, Active: true, MemMB: 8192, VPCPUs: 4, MaxConcurrency: 10})
	if err != nil {
		t.Fatal(err)
	}
	app, err := st.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "regional-sources", ComputeRegion: id, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	return st, app, node
}

func sourceLocalBackend(t *testing.T) *storage.LocalStorageBackend {
	t.Helper()
	be, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return be
}

func TestRegionalBuilderMaterializesCommittedSourceBeforeVM(t *testing.T) {
	for _, scenario := range []string{"download", "corrupt archive", "removed route with local archive"} {
		t.Run(scenario, func(t *testing.T) {
			st, app, node := regionalSourceFixture(t)
			root := t.TempDir()
			path := filepath.Join(t.TempDir(), "api-source.tar.gz")
			makeTarballWithName(t, path, []string{"package.json", "index.js"})
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256(data)
			// The committed object intentionally differs from the queue's build ID.
			key := region.ArtifactKey(app.ComputeRegion, "sources/"+uuid.NewString()+".tar.gz")
			dep, err := st.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, SourcePath: path, SourceBytes: int64(len(data)), SourceSHA256: hex.EncodeToString(hash[:]), SourceStorageKey: key})
			if err != nil {
				t.Fatal(err)
			}
			build, err := st.CreateBuild(t.Context(), dep.ID, dep.Kind, dep.SourceBytes, "")
			if err != nil {
				t.Fatal(err)
			}
			workerPath := filepath.Join(root, "sources", build.ID+".tar.gz")
			be := sourceLocalBackend(t)
			routes := map[string]storage.StorageBackend{app.ComputeRegion: be}
			if scenario == "removed route with local archive" {
				delete(routes, app.ComputeRegion)
				if err := os.MkdirAll(filepath.Dir(workerPath), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(workerPath, data, 0o640); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "corrupt archive" {
				data[0] ^= 0xff
			}
			router, err := storage.NewRegionRouter(routes, sourceLocalBackend(t))
			if err != nil {
				t.Fatal(err)
			}
			if len(routes) > 0 {
				if err := router.Put(t.Context(), key, strings.NewReader(string(data))); err != nil {
					t.Fatal(err)
				}
			}
			output := filepath.Join(t.TempDir(), "produced.ext4")
			if err := os.WriteFile(output, []byte("produced layer"), 0o640); err != nil {
				t.Fatal(err)
			}
			vm := &fakeVM{out: BuildOutcome{OCIImage: output, ExitCode: 0}}
			b := New(st, &fakeNotifier{}, vm, NewCache(t.TempDir()), NewDetector(), nil, Config{BuilderNodeID: node.Name, SourceSpoolDir: root, CacheAffinityGrace: -time.Second}, quietLogger()).WithSourceStorage(router)
			_, err = b.ProcessOne(t.Context(), build.ID)
			if scenario == "download" {
				if err != nil || vm.spawnCalls != 1 {
					t.Fatalf("regional download did not reach VM: calls=%d err=%v", vm.spawnCalls, err)
				}
				got, err := os.ReadFile(workerPath)
				if err != nil || string(got) != string(data) {
					t.Fatalf("materialized archive differs: %v", err)
				}
				if vm.lastRequest.SourcePath != workerPath {
					t.Fatalf("VM received API path: %q", vm.lastRequest.SourcePath)
				}
				persisted, err := st.DeploymentByID(t.Context(), dep.ID)
				if err != nil || persisted.SourcePath != path {
					t.Fatalf("worker changed durable API path: %+v %v", persisted, err)
				}
			} else {
				if err == nil || vm.spawnCalls != 0 {
					t.Fatalf("invalid source reached VM: calls=%d err=%v", vm.spawnCalls, err)
				}
				if scenario == "removed route with local archive" {
					if !errors.Is(err, storage.ErrRegionNotConfigured) {
						t.Fatalf("removed route: %v", err)
					}
					row, err := st.BuildByID(t.Context(), build.ID)
					if err != nil || row.Status != state.BuildQueued {
						t.Fatalf("route failure did not requeue: %+v %v", row, err)
					}
				}
			}
		})
	}
}

func TestRegionalSourceGCRetainsPendingReferencesAndFullNamespaces(t *testing.T) {
	st, app, _ := regionalSourceFixture(t)
	now := time.Now()
	id := sourceIDAt(now.Add(-48 * time.Hour))
	seedSourceBuild(t, st, id, true)
	legacy, eu, us := sourceLocalBackend(t), sourceLocalBackend(t), sourceLocalBackend(t)
	router, err := storage.NewRegionRouter(map[string]storage.StorageBackend{app.ComputeRegion: eu, "us-source": us}, legacy)
	if err != nil {
		t.Fatal(err)
	}
	kept := region.ArtifactKey(app.ComputeRegion, sourceKey(id))
	for _, key := range []string{sourceKey(id), kept, region.ArtifactKey("us-source", sourceKey(id))} {
		if err := router.Put(t.Context(), key, strings.NewReader("archive")); err != nil {
			t.Fatal(err)
		}
	}
	dep, err := st.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, SourceStorageKey: kept})
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := SourceGCSweepOnce(t.Context(), router, st, 24*time.Hour, now, quietLogger())
	if err != nil || deleted != 2 {
		t.Fatalf("regional retention: deleted=%d err=%v", deleted, err)
	}
	rc, err := router.Get(t.Context(), kept)
	if err != nil {
		t.Fatalf("pending retry source was deleted: %v", err)
	}
	_ = rc.Close()
	if err := st.FailSourceDeployment(t.Context(), dep.ID, "test"); err != nil {
		t.Fatal(err)
	}
	deleted, err = SourceGCSweepOnce(t.Context(), router, st, 24*time.Hour, now, quietLogger())
	if err != nil || deleted != 1 {
		t.Fatalf("completed retention: deleted=%d err=%v", deleted, err)
	}
}

type sourceListOutage struct{ *storage.LocalStorageBackend }

func (sourceListOutage) List(context.Context, string) ([]string, error) {
	return nil, errors.New("regional inventory offline")
}

type sourceReferencesOutage struct{ state.Store }

func (sourceReferencesOutage) SourceArtifactReferencedByPendingBuild(context.Context, string) (bool, error) {
	return false, errors.New("reference lookup offline")
}

func TestRegionalSourceGCIncompleteInventoryOrLookupDeletesNothing(t *testing.T) {
	for _, scenario := range []string{"inventory outage", "reference outage", "missing reference capability"} {
		t.Run(scenario, func(t *testing.T) {
			st := state.NewMemStore()
			legacy := sourceLocalBackend(t)
			key := sourceKey(sourceIDAt(time.Now().Add(-48 * time.Hour)))
			if err := legacy.Put(t.Context(), key, strings.NewReader("source")); err != nil {
				t.Fatal(err)
			}
			var store state.Store = st
			routes := map[string]storage.StorageBackend{}
			switch scenario {
			case "inventory outage":
				routes["eu-source"] = sourceListOutage{sourceLocalBackend(t)}
			case "reference outage":
				store = sourceReferencesOutage{st}
			case "missing reference capability":
				store = struct{ state.Store }{st}
			}
			router, err := storage.NewRegionRouter(routes, legacy)
			if err != nil {
				t.Fatal(err)
			}
			deleted, err := SourceGCSweepOnce(t.Context(), router, store, time.Hour, time.Now(), quietLogger())
			if err == nil || deleted != 0 {
				t.Fatalf("incomplete pass deleted objects: %d %v", deleted, err)
			}
			rc, err := legacy.Get(t.Context(), key)
			if err != nil {
				t.Fatalf("source disappeared: %v", err)
			}
			_, _ = io.Copy(io.Discard, rc)
			_ = rc.Close()
		})
	}
}
