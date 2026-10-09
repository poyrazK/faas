package imaged

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// production-us hunt #8: a source deploy with a companion built its app
// layer through buildLocalOCIAppLayer, which never built companion layers,
// so snapshot prime failed with `sidecar "heartbeat" has no built layer`.
// Only registry-image deploys (buildImageLayer) built them.
func TestBuildLocalOCIAppLayerBuildsCompanionLayers(t *testing.T) {
	cfg, err := json.Marshal(map[string]any{
		"architecture": "amd64",
		"os":           "linux",
		"config":       map[string]any{"Cmd": []string{"node", "server.js"}},
		"rootfs":       map[string]any{"type": "layers", "diff_ids": []string{"sha256:" + strings.Repeat("1", 64)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	layer := gzipBytes(t, []byte("built app layer"))
	cfgDigest := digestFor(t, cfg)
	layerDigest := digestFor(t, layer)
	manifest := minimalManifestBytes(cfgDigest, []string{layerDigest})
	manifestDigest := digestFor(t, manifest)
	archive := buildLocalOCIArchive(t, map[string][]byte{
		"index.json":             minimalIndexBytes(manifestDigest),
		blobPath(manifestDigest): manifest,
		blobPath(cfgDigest):      cfg,
		blobPath(layerDigest):    layer,
	})

	heartbeatRef := "docker.io/library/busybox@sha256:" + strings.Repeat("b", 64)
	heartbeatLayer := "sha256:" + strings.Repeat("7", 64)
	mp := &fakeManifestPuller{
		sidecarManifests: map[string]oci.Manifest{
			heartbeatRef: {Layers: []oci.Descriptor{{Digest: heartbeatLayer, Size: 128}}},
		},
		layerBlobs: map[string][]byte{
			heartbeatLayer: gzTar(t, map[string]string{"bin/sh": "#!/bin/sh\n"}),
		},
	}
	sidecars, err := json.Marshal([]map[string]any{
		{"name": "heartbeat", "image": heartbeatRef, "type": "sidecar", "cmd": []string{"sh", "-c", "sleep 1"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	h := newFunctionTestHarness(t, api.PlanPro, "")
	dep, err := h.store.CreateDeployment(context.Background(), state.Deployment{
		AppID: h.app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployImaging, SourcePath: h.dep.SourcePath, Sidecars: sidecars,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetDeploymentRootfs(context.Background(), dep.ID, archive, "builder/companion-source", 1); err != nil {
		t.Fatal(err)
	}
	dep, err = h.store.DeploymentByID(context.Background(), dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	h.dep = dep
	handler := New(h.store, h.notif, mp, h.bld, "./init", h.appsR, silentLogger())
	if err := handler.buildLocalOCIAppLayer(context.Background(), h.app, &h.dep, h.acct); err != nil {
		t.Fatalf("buildLocalOCIAppLayer: %v", err)
	}
	if h.dep.ImageDigest != manifestDigest {
		t.Fatalf("source digest = %q, want %q", h.dep.ImageDigest, manifestDigest)
	}
	rows, err := h.store.ListDeploymentSidecarLayers(context.Background(), h.dep.ID)
	if err != nil {
		t.Fatalf("list sidecar layers: %v", err)
	}
	if len(rows) != 1 || rows[0].SidecarName != "heartbeat" {
		t.Fatalf("companion layers = %+v, want one heartbeat layer", rows)
	}
	if want := sched.AppSidecarLayerKey(h.app.Slug, h.dep.ID, "heartbeat"); rows[0].StorageKey != want {
		t.Fatalf("heartbeat StorageKey = %q, want %q", rows[0].StorageKey, want)
	}
}
