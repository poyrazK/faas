package builderd

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/region"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

type exportPutBackend struct {
	storage.StorageBackend
	fail  error
	short bool
	hook  func()
}

func (s *exportPutBackend) Put(ctx context.Context, key string, r io.Reader) error {
	if s.fail != nil {
		return s.fail
	}
	if s.short {
		_, err := io.Copy(io.Discard, io.LimitReader(r, 1))
		return err
	}
	if err := s.StorageBackend.Put(ctx, key, r); err != nil {
		return err
	}
	if s.hook != nil {
		s.hook()
	}
	return nil
}

func TestRegionalBuildExportPublicationFences(t *testing.T) {
	for _, scenario := range []string{"success", "storage failure", "short upload", "cancelled during upload", "reaped during upload"} {
		t.Run(scenario, func(t *testing.T) {
			st, app, node := regionalSourceFixture(t)
			acct, err := st.AccountByID(t.Context(), app.AccountID)
			if err != nil {
				t.Fatal(err)
			}
			dep, err := st.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployBuilding})
			if err != nil {
				t.Fatal(err)
			}
			build, err := st.CreateBuild(t.Context(), dep.ID, dep.Kind, 1, "")
			if err != nil {
				t.Fatal(err)
			}
			claim, err := st.ClaimQueuedBuildWithNodeAffinity(t.Context(), build.ID, node.Name, 0)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "image.tar")
			body := "customer OCI export"
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			backend := &exportPutBackend{StorageBackend: sourceLocalBackend(t)}
			router, err := storage.NewRegionRouter(map[string]storage.StorageBackend{app.ComputeRegion: backend}, sourceLocalBackend(t))
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "storage failure":
				backend.fail = errors.New("registry unavailable")
			case "short upload":
				backend.short = true
			case "cancelled during upload":
				backend.hook = func() {
					if err := st.UpdateBuildStatus(t.Context(), build.ID, state.BuildCancelled, "", false, true); err != nil {
						t.Error(err)
					}
				}
			case "reaped during upload":
				backend.hook = func() {
					if err := st.UpdateBuildStatus(t.Context(), build.ID, state.BuildFailed, state.FailureTimeout, false, true); err != nil {
						t.Error(err)
					}
				}
			}
			notif := &fakeNotifier{}
			b := New(st, notif, &fakeVM{}, nil, NewDetector(), nil, Config{BuilderNodeID: node.Name}, quietLogger()).WithSourceStorage(router)
			result, err := b.completeBuild(t.Context(), claim, dep, app, acct, "abc", "", BuildResult{BuildID: build.ID, LayerPath: path, LayerBytes: int64(len(body))}, time.Now())
			d, readErr := st.DeploymentByID(t.Context(), dep.ID)
			if readErr != nil {
				t.Fatal(readErr)
			}
			row, _ := st.BuildByID(t.Context(), build.ID)
			if scenario == "success" {
				if err != nil || result.BuildID != build.ID || row.Status != state.BuildSucceeded || !d.BuildArtifactReference().ValidFor(app.ComputeRegion) {
					t.Fatalf("completion: %+v %+v %v", result, d, err)
				}
				r, err := router.Get(t.Context(), d.BuildArtifactKey)
				if err != nil {
					t.Fatal(err)
				}
				got, err := io.ReadAll(r)
				_ = r.Close()
				if err != nil || string(got) != body {
					t.Fatalf("published bytes: %q %v", got, err)
				}
			} else {
				if d.BuildArtifactKey != "" || d.RootfsPath != "" || result.BuildID != "" {
					t.Fatal("failed/stale upload committed an artifact")
				}
				if _, err := st.BuildProvenanceByBuildID(t.Context(), build.ID); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("stale provenance: %v", err)
				}
				if scenario == "storage failure" || scenario == "short upload" {
					if err == nil || row.Status != state.BuildQueued {
						t.Fatalf("publication failure was not requeued: %s %v", row.Status, err)
					}
				}
			}
		})
	}
}

func TestRegionalBuildExportGCProtectsFullReferences(t *testing.T) {
	st, app, node := regionalSourceFixture(t)
	dep, err := st.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployBuilding})
	if err != nil {
		t.Fatal(err)
	}
	build, err := st.CreateBuild(t.Context(), dep.ID, dep.Kind, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	claim, err := st.ClaimQueuedBuildWithNodeAffinity(t.Context(), build.ID, node.Name, 0)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	digest := strings.Repeat("a", 64)
	oldPub := sourceIDAt(now.Add(-48 * time.Hour))
	active := region.BuildExportKey(app.ComputeRegion, oldPub, digest)
	sameInOtherRegion := region.BuildExportKey("us-export", oldPub, digest)
	fresh := region.BuildExportKey(app.ComputeRegion, sourceIDAt(now), digest)
	backend, other := sourceLocalBackend(t), sourceLocalBackend(t)
	router, err := storage.NewRegionRouter(map[string]storage.StorageBackend{app.ComputeRegion: backend, "us-export": other}, sourceLocalBackend(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{active, sameInOtherRegion, fresh} {
		if err := router.Put(t.Context(), key, strings.NewReader("x")); err != nil {
			t.Fatal(err)
		}
	}
	ref := state.BuildArtifactReference{Key: active, SHA256: digest, Bytes: 1}
	prov := state.BuildProvenance{BuildID: claim.ID, BuilderNodeID: node.Name, StartedAt: claim.StartedAt, FinishedAt: now}
	if err := st.CompleteBuildWithArtifact(t.Context(), claim, "/producer/image.tar", "runtime-key", 1, prov, ref); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateDeploymentStatus(t.Context(), dep.ID, state.DeployImaging, ""); err != nil {
		t.Fatal(err)
	}
	n, err := BuildArtifactGCSweepOnce(t.Context(), router, st, 24*time.Hour, now)
	if err != nil || n != 1 {
		t.Fatalf("sweep: %d %v", n, err)
	}
	r, err := router.Get(t.Context(), active)
	if err != nil {
		t.Fatalf("active export removed: %v", err)
	}
	_ = r.Close()
	if _, err := router.Get(t.Context(), sameInOtherRegion); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("full-key orphan retained: %v", err)
	}
	if err := st.UpdateDeploymentStatus(t.Context(), dep.ID, state.DeployFailed, "done"); err != nil {
		t.Fatal(err)
	}
	n, err = BuildArtifactGCSweepOnce(t.Context(), router, st, 24*time.Hour, now)
	if err != nil || n != 1 {
		t.Fatalf("terminal sweep: %d %v", n, err)
	}
	if _, err := uuid.Parse(oldPub); err != nil {
		t.Fatal(err)
	}
}

// Reference and inventory errors abort the planning pass before deletion.
type exportReferenceFailureStore struct {
	*state.MemStore
	calls int
}

func (s *exportReferenceFailureStore) BuildArtifactReferencedForMaterialization(context.Context, string) (bool, error) {
	s.calls++
	if s.calls == 2 {
		return false, errors.New("database unavailable")
	}
	return false, nil
}
func TestRegionalBuildExportGCAbortsIncompletePlan(t *testing.T) {
	for _, scenario := range []string{"reference error", "incomplete inventory"} {
		t.Run(scenario, func(t *testing.T) {
			now := time.Now()
			digest := strings.Repeat("a", 64)
			backend := sourceLocalBackend(t)
			keys := []string{region.BuildExportKey("eu-export", sourceIDAt(now.Add(-48*time.Hour)), digest), region.BuildExportKey("eu-export", sourceIDAt(now.Add(-49*time.Hour)), digest)}
			routes := map[string]storage.StorageBackend{"eu-export": backend}
			if scenario == "incomplete inventory" {
				routes["us-export"] = noListBackend{}
			}
			router, err := storage.NewRegionRouter(routes, sourceLocalBackend(t))
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range keys {
				if err := router.Put(t.Context(), key, strings.NewReader("x")); err != nil {
					t.Fatal(err)
				}
			}
			store := &exportReferenceFailureStore{MemStore: state.NewMemStore()}
			n, err := BuildArtifactGCSweepOnce(t.Context(), router, store, 24*time.Hour, now)
			if err == nil || n != 0 {
				t.Fatalf("incomplete pass deleted: %d %v", n, err)
			}
			for _, key := range keys {
				r, err := router.Get(t.Context(), key)
				if err != nil {
					t.Fatalf("object removed after incomplete plan: %s %v", key, err)
				}
				_ = r.Close()
			}
		})
	}
}
