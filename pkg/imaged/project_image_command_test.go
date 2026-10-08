// adr: 678, 680, 682, 683
package imaged

import (
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectImageCommandRetainsOCIEntrypoint(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command []string
		want    []string
	}{
		{"inherited", nil, []string{"/entrypoint", "original"}},
		{"exec arguments", []string{"serve", "argument with spaces"}, []string{"/entrypoint", "serve", "argument with spaces"}},
		{"shell command", []string{"/bin/sh", "-c", "exec /app/worker"}, []string{"/entrypoint", "/bin/sh", "-c", "exec /app/worker"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := oci.ImageConfig{Entrypoint: []string{"/entrypoint"}, Cmd: []string{"original"}}
			app := state.App{StartCommand: "must not replace entrypoint", Manifest: state.AppManifest{
				ProjectImage: "example.com/app:v1", ProjectImageCommand: tc.command}}
			manifest, err := manifestFromImageConfigWithDeployment(config, app, state.Deployment{})
			if err != nil || !reflect.DeepEqual(manifest.Entrypoint, tc.want) {
				t.Fatalf("argv = %v, %v; want %v", manifest.Entrypoint, err, tc.want)
			}
			if config.Cmd[0] != "original" {
				t.Fatal("mutated the image defaults")
			}
		})
	}
}

func TestProjectImageFullRootfsUsesPinnedChild(t *testing.T) {
	store := state.NewMemStore()
	acct, err := store.CreateAccount(t.Context(), "project-image@example.com", "pro")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "project-image", RAMMB: 512,
		Manifest: state.AppManifest{ProjectImage: "example.com/app:v1", ProjectImageCommand: []string{"serve", "with spaces"},
			ProjectImageHealthcheck: &api.ComposeHealthcheck{Test: []string{"CMD", "/accepted-check"}, TimeoutNS: 250000000}}})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := frameworkprofile.CaptureImageRuntime(app.Manifest.ProjectImageCommand, app.Manifest.ProjectImageHealthcheck)
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
		ImageDigest: app.Manifest.ProjectImage, FullRootfsAllowAuto: true, OverridePort: 3000, InferredProfile: profile})
	if err != nil {
		t.Fatal(err)
	}
	// A source reapply can remove the current image declaration while the
	// original image deployment is still queued. Its accepted CMD stays valid.
	nextManifest := app.Manifest
	nextManifest.ProjectImage = ""
	nextManifest.ProjectImageCommand = nil
	nextManifest.ProjectImageHealthcheck = nil
	start := "must not replace original image command"
	if _, err := store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &nextManifest, StartCommand: &start}); err != nil {
		t.Fatal(err)
	}
	source := "example.com/app@sha256:" + strings.Repeat("a", 64)
	child := "example.com/app@sha256:" + strings.Repeat("b", 64)
	configDigest := "sha256:" + strings.Repeat("c", 64)
	baseConfigDigest := "sha256:" + strings.Repeat("d", 64)
	layer := "sha256:" + strings.Repeat("e", 64)
	puller := &fakeManifestPuller{appRef: child,
		appManifest:  oci.Manifest{Config: oci.Descriptor{Digest: configDigest}, Layers: []oci.Descriptor{{Digest: layer}}},
		appConfig:    oci.Config{Entrypoint: []string{"/entrypoint"}, Cmd: []string{"original"}, DiffIDs: []string{"app-layer"}},
		baseManifest: oci.Manifest{Config: oci.Descriptor{Digest: baseConfigDigest}},
		baseConfig:   oci.Config{DiffIDs: []string{"base-one", "base-two"}}, layerBlobs: map[string][]byte{}}
	puller.putConfig(configDigest, puller.appConfig)
	puller.putConfig(baseConfigDigest, puller.baseConfig)
	puller.layerBlobs[layer] = gzTar(t, map[string]string{"entrypoint": "#!/bin/sh\n"})
	resolver := &resolvingTestPuller{fakeManifestPuller: puller, resolution: oci.ImageResolution{
		SourceReference: source, Reference: child, Digest: "sha256:" + strings.Repeat("b", 64)}}
	builder := &fakeBuilder{}
	notifier := &fakeNotifier{}
	handler := New(store, notifier, &frozenCommandPuller{resolver}, builder, "/tmp/guest-init", t.TempDir(), silentLogger())
	if err := handler.HandleNotification(t.Context(), db.Notification{Channel: db.NotifyDeploymentChanged,
		Payload: `{"app_id":"` + app.ID + `","to":"` + dep.ID + `","kind":"image"}`}); err != nil {
		t.Fatal(err)
	}
	got, err := store.DeploymentByID(t.Context(), dep.ID)
	if err != nil || got.Status != state.DeploySnapshotting || got.ImageDigest != source || len(builder.fullRootfsCalls) != 1 {
		t.Fatalf("image materialization = %+v, %v; full-rootfs calls=%d", got, err, len(builder.fullRootfsCalls))
	}
	if !frameworkprofile.RequiresImageHealthcheck(got.InferredProfile) {
		t.Fatal("effective image healthcheck was not handed to host readiness")
	}
	if argv := builder.fullRootfsCalls[0].Manifest.Entrypoint; !reflect.DeepEqual(argv, []string{"/entrypoint", "serve", "with spaces"}) {
		t.Fatalf("queued image used later app command: %v", argv)
	}
	if check := builder.fullRootfsCalls[0].Manifest.Healthcheck; check == nil || check.Test[1] != "/accepted-check" || check.ImageTiming.TimeoutNS != 250000000 {
		t.Fatalf("queued image lost its accepted healthcheck: %+v", check)
	}
	if len(resolver.configRefs) != 1 || resolver.configRefs[0] != child || len(resolver.manifestRefs) < 2 {
		t.Fatalf("reads = configs:%v manifests:%v", resolver.configRefs, resolver.manifestRefs)
	}
	for _, ref := range resolver.manifestRefs {
		if ref == app.Manifest.ProjectImage {
			t.Fatal("full-rootfs fallback reread the mutable tag")
		}
	}
}
