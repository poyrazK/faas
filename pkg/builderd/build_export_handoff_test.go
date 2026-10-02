package builderd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/imaged"
	"github.com/onebox-faas/faas/pkg/region"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

type handoffLayerBuilder struct {
	imaged.LayerBuilder
	calls    int
	function bool
}

func (b *handoffLayerBuilder) Build(ctx context.Context, in rootfs.BuildInput) (rootfs.BuildResult, error) {
	b.calls++
	if len(in.Layers) == 0 {
		return rootfs.BuildResult{}, fmt.Errorf("no OCI layers reached image assembly")
	}
	for _, r := range in.Layers {
		if _, err := io.Copy(io.Discard, r); err != nil {
			return rootfs.BuildResult{}, err
		}
	}
	if b.function && (in.TarballPath != "" || in.FunctionHandlerSourcePath == "" || in.FunctionRunnerPath == "") {
		return rootfs.BuildResult{}, fmt.Errorf("function assembly did not consume the built artifact")
	}
	if err := in.Storage.Put(ctx, in.StorageKey, bytes.NewBufferString("regional runtime ext4")); err != nil {
		return rootfs.BuildResult{}, err
	}
	return rootfs.BuildResult{ContentBytes: 21, ImageKey: in.StorageKey, RunnerDigest: "sha256:runner"}, nil
}

func handoffTar(t *testing.T, entries map[string][]byte, gz bool) []byte {
	t.Helper()
	var out bytes.Buffer
	var writer io.Writer = &out
	var zw *gzip.Writer
	if gz {
		zw = gzip.NewWriter(&out)
		writer = zw
	}
	tw := tar.NewWriter(writer)
	for name, data := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if zw != nil {
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return out.Bytes()
}

func handoffOCI(t *testing.T) []byte {
	t.Helper()
	layer := handoffTar(t, map[string][]byte{"app/server": []byte("built Go executable")}, true)
	config, _ := json.Marshal(map[string]any{"architecture": "amd64", "os": "linux", "config": map[string]any{"Entrypoint": []string{"/app/server"}, "Env": []string{"PORT=8080"}}, "rootfs": map[string]any{"type": "layers", "diff_ids": []string{"sha256:" + stringsOfHex("a")}}})
	digest := func(b []byte) string { sum := sha256.Sum256(b); return "sha256:" + hex.EncodeToString(sum[:]) }
	desc := func(kind string, b []byte) map[string]any {
		return map[string]any{"mediaType": kind, "digest": digest(b), "size": len(b)}
	}
	manifest, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json", "config": desc("application/vnd.oci.image.config.v1+json", config), "layers": []any{desc("application/vnd.oci.image.layer.v1.tar+gzip", layer)}})
	index, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": "application/vnd.oci.image.index.v1+json", "manifests": []any{desc("application/vnd.oci.image.manifest.v1+json", manifest)}})
	blob := func(b []byte) string { return "blobs/sha256/" + digest(b)[7:] }
	return handoffTar(t, map[string][]byte{"index.json": index, blob(manifest): manifest, blob(config): config, blob(layer): layer}, false)
}
func stringsOfHex(s string) string {
	var out string
	for i := 0; i < 64; i++ {
		out += s
	}
	return out
}

func TestRegionalBuildExportConnectedPeerHandoff(t *testing.T) {
	for _, function := range []bool{false, true} {
		t.Run(fmt.Sprintf("function=%t", function), func(t *testing.T) {
			st, app, producer := regionalSourceFixture(t)
			if function {
				original := app
				original.ID, original.Slug = "", "function-export"
				original.Type, original.Runtime = state.AppTypeFunction, imaged.RuntimeGo124
				var err error
				app, err = st.CreateApp(t.Context(), original)
				if err != nil {
					t.Fatal(err)
				}
			}
			peerSpec := producer
			peerSpec.ID, peerSpec.Name = "", "image-peer"
			peer, err := st.CreateComputeNode(t.Context(), peerSpec)
			if err != nil {
				t.Fatal(err)
			}
			peerSpec.ID, peerSpec.Name = "", "image-peer-two"
			second, err := st.CreateComputeNode(t.Context(), peerSpec)
			if err != nil {
				t.Fatal(err)
			}
			regional, legacy := sourceLocalBackend(t), sourceLocalBackend(t)
			router, err := storage.NewRegionRouter(map[string]storage.StorageBackend{app.ComputeRegion: regional}, legacy)
			if err != nil {
				t.Fatal(err)
			}
			source := handoffTar(t, map[string][]byte{"Dockerfile": []byte("FROM scratch\n")}, true)
			sourceID, err := uuid.NewV7()
			if err != nil {
				t.Fatal(err)
			}
			sourceKey := region.ArtifactKey(app.ComputeRegion, "sources/"+sourceID.String()+".tar.gz")
			if err := router.Put(t.Context(), sourceKey, bytes.NewReader(source)); err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(source)
			dep, err := st.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindDockerfile, SourcePath: "/missing/api/source.tar.gz", SourceStorageKey: sourceKey, SourceBytes: int64(len(source)), SourceSHA256: hex.EncodeToString(sum[:])})
			if err != nil {
				t.Fatal(err)
			}
			build, err := st.CreateBuild(t.Context(), dep.ID, dep.Kind, dep.SourceBytes, "")
			if err != nil {
				t.Fatal(err)
			}
			export := filepath.Join(t.TempDir(), build.ID, "build", "out", "image.tar")
			if err := os.MkdirAll(filepath.Dir(export), 0750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(export, handoffOCI(t), 0600); err != nil {
				t.Fatal(err)
			}
			producerNotif := &fakeNotifier{}
			vm := &fakeVM{out: BuildOutcome{OCIImage: export, ExitCode: 0}}
			b := New(st, producerNotif, vm, NewCache(t.TempDir()), NewDetector(), nil, Config{BuilderNodeID: producer.Name, SourceSpoolDir: t.TempDir(), CacheAffinityGrace: -time.Second}, quietLogger()).WithSourceStorage(router)
			result, err := b.ProcessOne(t.Context(), build.ID)
			if err != nil || result.BuildID != build.ID || vm.spawnCalls != 1 {
				t.Fatalf("builder completion: %+v %v", result, err)
			}
			committed, err := st.DeploymentByID(t.Context(), dep.ID)
			if err != nil || !committed.BuildArtifactReference().ValidFor(app.ComputeRegion) {
				t.Fatalf("missing durable export: %+v %v", committed, err)
			}
			if err := os.Remove(export); err != nil {
				t.Fatal(err)
			}
			if err := st.SetComputeNodeActive(t.Context(), producer.ID, false); err != nil {
				t.Fatal(err)
			}
			layerBuilder := &handoffLayerBuilder{function: function}
			imageNotif := &fakeNotifier{}
			newHandler := func(name string) *imaged.Handler {
				return imaged.New(st, imageNotif, nil, layerBuilder, "./init", t.TempDir(), quietLogger()).WithStorage(router).WithNodeName(name).WithFunctionRunnerGo124("./runner-go")
			}
			firstHandler, secondHandler := newHandler(peer.Name), newHandler(second.Name)
			payload, _ := json.Marshal(map[string]string{"app_id": app.ID, "deployment_id": dep.ID, "node_id": producer.Name})
			var wg sync.WaitGroup
			errs := make(chan error, 2)
			for _, h := range []*imaged.Handler{firstHandler, secondHandler} {
				wg.Add(1)
				go func() {
					defer wg.Done()
					errs <- h.HandleNotification(t.Context(), db.Notification{Channel: db.NotifySnapshotBoot, Payload: string(payload)})
				}()
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				if err != nil {
					t.Fatal(err)
				}
			}
			final, err := st.DeploymentByID(t.Context(), dep.ID)
			want := region.ArtifactKey(app.ComputeRegion, sched.AppLayerKey(app.Slug, dep.ID))
			if err != nil || final.Status != state.DeploySnapshotting || final.RootfsKey != want || final.BuildArtifactReference() != committed.BuildArtifactReference() || layerBuilder.calls != 1 {
				t.Fatalf("peer handoff: %+v calls=%d err=%v", final, layerBuilder.calls, err)
			}
			if _, err := legacy.Get(t.Context(), sched.AppLayerKey(app.Slug, dep.ID)); err == nil {
				t.Fatal("runtime artifact escaped selected region")
			}
			found := false
			for _, call := range imageNotif.calls {
				if call.channel == db.NotifySnapshotPrime {
					found = true
				}
			}
			if !found {
				t.Fatal("peer did not hand off to scheduler")
			}
		})
	}
}
