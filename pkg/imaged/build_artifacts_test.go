package imaged

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/region"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

type imageExportBackend struct {
	storage.StorageBackend
	exportGets int
	fail       error
	hook       func()
	key        string
}

func (s *imageExportBackend) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if key == s.key {
		s.exportGets++
		if s.fail != nil {
			return nil, s.fail
		}
		if s.hook != nil {
			s.hook()
		}
	}
	return s.StorageBackend.Get(ctx, key)
}

type retryPrimeNotifier struct {
	fakeNotifier
	fail bool
}

func (n *retryPrimeNotifier) Notify(ctx context.Context, channel, payload string) error {
	if n.fail && channel == db.NotifySnapshotPrime {
		return errors.New("notification unavailable")
	}
	return n.fakeNotifier.Notify(ctx, channel, payload)
}

func regionalImageExportFixture(t *testing.T) (*testHarness, *Handler, *imageExportBackend, state.ComputeNode, []byte) {
	t.Helper()
	fx := newTestHarness(t, state.DeploymentKindTarball, api.PlanPro, "")
	id := "eu-image-export"
	if _, err := fx.store.UpsertComputeRegion(t.Context(), state.ComputeRegion{ID: id, DisplayName: id, AcceptingNewApps: true}); err != nil {
		t.Fatal(err)
	}
	owner, err := fx.store.CreateComputeNode(t.Context(), state.ComputeNode{Name: "export-owner", Region: &id, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	peer, err := fx.store.CreateComputeNode(t.Context(), state.ComputeNode{Name: "export-peer", Region: &id, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	app := fx.app
	app.ID, app.Slug, app.NodeID, app.ComputeRegion = "", "export-image", "", id
	app, err = fx.store.CreateApp(t.Context(), app)
	if err != nil {
		t.Fatal(err)
	}
	dep, err := fx.store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployBuilding})
	if err != nil {
		t.Fatal(err)
	}
	build, err := fx.store.CreateBuild(t.Context(), dep.ID, dep.Kind, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	claim, err := fx.store.ClaimQueuedBuildWithNodeAffinity(t.Context(), build.ID, owner.Name, 0)
	if err != nil {
		t.Fatal(err)
	}
	config := []byte(`{"architecture":"amd64","os":"linux","config":{"Entrypoint":["/app/server"],"Env":["PORT=8080"]},"rootfs":{"type":"layers","diff_ids":["sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"]}}`)
	layer := gzipBytes(t, []byte("OCI layer payload"))
	configDigest, layerDigest := digestFor(t, config), digestFor(t, layer)
	manifest := minimalManifestBytes(configDigest, []string{layerDigest})
	manifestDigest := digestFor(t, manifest)
	archive := buildLocalOCIArchive(t, map[string][]byte{"index.json": minimalIndexBytes(manifestDigest), blobPath(configDigest): config, blobPath(layerDigest): layer, blobPath(manifestDigest): manifest})
	data, err := io.ReadAll(mustOpenExport(t, archive))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	pub, _ := uuid.NewV7()
	ref := state.BuildArtifactReference{Key: region.BuildExportKey(id, pub.String(), hash), SHA256: hash, Bytes: int64(len(data))}
	back := &imageExportBackend{StorageBackend: mustLocalStorage(t, t.TempDir()), key: strings.TrimPrefix(ref.Key, "regions/"+id+"/")}
	router, err := storage.NewRegionRouter(map[string]storage.StorageBackend{id: back}, mustLocalStorage(t, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if err := router.Put(t.Context(), ref.Key, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if err := fx.store.CompleteBuildWithArtifact(t.Context(), claim, "/missing/producer/image.tar", region.ArtifactKey(id, sched.AppLayerKey(app.Slug, dep.ID)), ref.Bytes, state.BuildProvenance{BuildID: build.ID, BuilderNodeID: owner.Name, StartedAt: claim.StartedAt, FinishedAt: time.Now()}, ref); err != nil {
		t.Fatal(err)
	}
	dep, err = fx.store.DeploymentByID(t.Context(), dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	fx.app, fx.dep = app, dep
	h := New(fx.store, fx.notif, nil, fx.bld, "./init", t.TempDir(), silentLogger()).WithStorage(router).WithNodeName(peer.Name)
	return fx, h, back, peer, data
}
func mustOpenExport(t *testing.T, path string) io.ReadCloser {
	t.Helper()
	// Archive parsing is performed by the production consumer; this helper
	// only reads the bytes that the fixture publishes.
	r, err := os.Open(path) //nolint:forbidigo // Fixture-owned OCI archive created in t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func exportNotification(dep state.Deployment) db.Notification {
	payload, _ := json.Marshal(snapshotBootPayload{AppID: dep.AppID, DeploymentID: dep.ID, NodeID: "export-owner"})
	return db.Notification{Channel: db.NotifySnapshotBoot, Payload: string(payload)}
}

func TestRegionalBuildExportConsumerBoundaries(t *testing.T) {
	for _, scenario := range []string{"wrong region", "inactive", "unnamed", "registry unavailable", "missing route", "corrupt", "short", "oversize", "cancelled during download"} {
		t.Run(scenario, func(t *testing.T) {
			fx, h, back, peer, data := regionalImageExportFixture(t)
			switch scenario {
			case "wrong region":
				h.WithNodeName(state.DefaultLocalNodeName)
			case "inactive":
				if err := fx.store.SetComputeNodeActive(t.Context(), peer.ID, false); err != nil {
					t.Fatal(err)
				}
			case "unnamed":
				h.WithNodeName("")
			case "registry unavailable":
				back.fail = errors.New("registry unavailable")
			case "missing route":
				router, err := storage.NewRegionRouter(map[string]storage.StorageBackend{}, mustLocalStorage(t, t.TempDir()))
				if err != nil {
					t.Fatal(err)
				}
				h.WithStorage(router)
			case "corrupt":
				data[0] ^= 0xff
			case "short":
				data = data[:len(data)-1]
			case "oversize":
				data = append(data, 0)
			case "cancelled during download":
				back.hook = func() {
					if err := fx.store.UpdateDeploymentStatus(t.Context(), fx.dep.ID, state.DeployCancelled, ""); err != nil {
						t.Error(err)
					}
				}
			}
			if scenario == "corrupt" || scenario == "short" || scenario == "oversize" {
				if err := h.storage.Put(t.Context(), fx.dep.BuildArtifactKey, bytes.NewReader(data)); err != nil {
					t.Fatal(err)
				}
			}
			err := h.HandleNotification(t.Context(), exportNotification(fx.dep))
			row, readErr := fx.store.DeploymentByID(t.Context(), fx.dep.ID)
			if readErr != nil {
				t.Fatal(readErr)
			}
			switch scenario {
			case "wrong region", "inactive", "unnamed":
				if !errors.Is(err, db.ErrNotificationNotOwned) || back.exportGets != 0 || row.Status != state.DeployBuilding {
					t.Fatalf("ineligible worker fetched: gets=%d status=%s err=%v", back.exportGets, row.Status, err)
				}
			case "registry unavailable", "missing route":
				if err == nil || row.Status != state.DeployBuilding {
					t.Fatalf("transient fetch terminalized work: status=%s err=%v", row.Status, err)
				}
			case "cancelled during download":
				if err == nil || row.Status != state.DeployCancelled {
					t.Fatalf("cancelled work resumed: status=%s err=%v", row.Status, err)
				}
			default:
				if !errors.Is(err, errInvalidBuildExport) || row.Status != state.DeployFailed {
					t.Fatalf("invalid export passed: status=%s err=%v", row.Status, err)
				}
			}
			if len(fx.bld.calls) != 0 {
				t.Fatal("rejected export reached rootfs conversion")
			}
		})
	}
}

func TestRegionalBuildExportRecoveryAndPrimeRetry(t *testing.T) {
	fx, h, back, _, _ := regionalImageExportFixture(t)
	// Simulate a process dying after its status advance but before conversion.
	if err := fx.store.UpdateDeploymentStatus(t.Context(), fx.dep.ID, state.DeployImaging, ""); err != nil {
		t.Fatal(err)
	}
	n := &retryPrimeNotifier{fail: true}
	h.notif = n
	loop := NewLoop(LoopConfig{Handler: h, Store: fx.store, Log: silentLogger()})
	loop.recoverBuildHandoffs(t.Context()) // no snapshot_boot was delivered.
	row, err := fx.store.DeploymentByID(t.Context(), fx.dep.ID)
	if err != nil || row.Status != state.DeploySnapshotting || len(fx.bld.calls) != 1 || back.exportGets != 1 {
		t.Fatalf("recovered conversion: %+v builds=%d gets=%d err=%v", row, len(fx.bld.calls), back.exportGets, err)
	}
	n.fail = false
	loop.recoverBuildHandoffs(t.Context())
	if len(fx.bld.calls) != 1 || back.exportGets != 1 {
		t.Fatal("prime retry reconverted or fetched OCI")
	}
	found := false
	for _, call := range n.calls {
		if call.channel == db.NotifySnapshotPrime {
			found = true
		}
	}
	if !found {
		t.Fatal("durable poll did not recover prime notification")
	}
}

func TestRegionalBuildExportSecurityScanRecoverySkipsOCI(t *testing.T) {
	fx, h, back, _, _ := regionalImageExportFixture(t)
	if err := fx.store.UpdateDeploymentStatus(t.Context(), fx.dep.ID, state.DeployImaging, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.store.AppendDeploymentStage(t.Context(), fx.dep.ID, state.StageSourceDownload, state.StageSecurityScan, time.Now(), ""); err != nil {
		t.Fatal(err)
	}
	key := region.ArtifactKey(fx.app.ComputeRegion, sched.AppLayerKey(fx.app.Slug, fx.dep.ID))
	if err := h.storage.Put(t.Context(), key, strings.NewReader("runtime layer")); err != nil {
		t.Fatal(err)
	}
	if err := fx.store.SetDeploymentRootfs(t.Context(), fx.dep.ID, "/consumer/final.ext4", key, 13); err != nil {
		t.Fatal(err)
	}
	back.fail = errors.New("the already-consumed export is unavailable")
	loop := NewLoop(LoopConfig{Handler: h, Store: fx.store, Log: silentLogger()})
	loop.recoverBuildHandoffs(t.Context())
	row, err := fx.store.DeploymentByID(t.Context(), fx.dep.ID)
	if err != nil || row.Status != state.DeploySnapshotting || back.exportGets != 0 || len(fx.bld.calls) != 0 {
		t.Fatalf("scan recovery rebuilt OCI: %+v gets=%d builds=%d err=%v", row, back.exportGets, len(fx.bld.calls), err)
	}
}
