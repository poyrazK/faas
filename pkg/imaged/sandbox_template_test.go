package imaged

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261027090013 step 10 part three — a sandbox template's source
// build is published as a full rootfs with every built layer, even when its
// image names no command; an ordinary source build keeps the two-drive
// layer.
func TestSandboxTemplateBuildIsAFullRootfs(t *testing.T) {
	rawLayer := []byte("template-layer")
	config := minimalConfigBytes(rawLayer) // no Cmd or Entrypoint
	layer := gzipBytes(t, rawLayer)
	manifest := minimalManifestBytes(config, layer)
	archive := buildLocalOCIArchive(t, map[string][]byte{
		"index.json": minimalIndexBytes(manifest), blobPath(digestFor(t, manifest)): manifest,
		blobPath(digestFor(t, config)): config, blobPath(digestFor(t, layer)): layer,
	})
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "template-build@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "sbt-0123456789ab", Type: state.AppTypeApp, SandboxTemplate: true,
		WorkloadClass: state.WorkloadClassJob, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeJob}})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindDockerfile, Status: state.DeployImaging})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, dep.ID, archive, "builder/source", 1); err != nil {
		t.Fatal(err)
	}
	if dep, err = store.DeploymentByID(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	builder := &fakeBuilder{bytesOut: 128}
	h := New(store, &fakeNotifier{}, nil, builder, "", t.TempDir(), silentLogger())
	if err := h.buildLocalOCIAppLayer(ctx, app, &dep, acct, nil); err != nil {
		t.Fatal(err)
	}
	if len(builder.fullRootfsCalls) != 1 || len(builder.calls) != 1 {
		t.Fatalf("builds: %d full-rootfs of %d", len(builder.fullRootfsCalls), len(builder.calls))
	}
	in := builder.fullRootfsCalls[0]
	if len(in.Layers) != 1 || in.Manifest.Entrypoint[0] != sandboxTemplateShell || in.Manifest.ExecutionMode != api.ExecutionModeJob {
		t.Fatalf("full-rootfs input: %d layers, manifest %+v", len(in.Layers), in.Manifest)
	}
	got, err := store.DeploymentByID(ctx, dep.ID)
	if err != nil || got.RootfsKey == "" {
		t.Fatalf("deployment rootfs %q, %v", got.RootfsKey, err)
	}
}
