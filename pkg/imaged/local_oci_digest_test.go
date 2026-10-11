package imaged

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSourceBuildPinsManifestBeforeReleaseAdmission(t *testing.T) {
	config := minimalConfigBytes()
	var document map[string]any
	if err := json.Unmarshal(config, &document); err != nil {
		t.Fatal(err)
	}
	document["config"] = map[string]any{"Cmd": []string{"/app/serve"}}
	config, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	layer := gzipBytes(t, []byte("source-build-layer"))
	manifest := minimalManifestBytes(digestFor(t, config), []string{digestFor(t, layer)})
	digest := digestFor(t, manifest)
	archive := buildLocalOCIArchive(t, map[string][]byte{
		"index.json": minimalIndexBytes(digest), blobPath(digest): manifest,
		blobPath(digestFor(t, config)): config, blobPath(digestFor(t, layer)): layer,
	})
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "source-release@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "source-release", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindDockerfile, Status: state.DeployImaging, ReleaseCommand: []string{"/app/migrate"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, dep.ID, archive, "builder/source", 1); err != nil {
		t.Fatal(err)
	}
	dep, err = store.DeploymentByID(ctx, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	h := New(store, &fakeNotifier{}, nil, &fakeBuilder{bytesOut: 128}, "", t.TempDir(), silentLogger())
	if err := h.buildLocalOCIAppLayer(ctx, app, &dep, acct); err != nil {
		t.Fatal(err)
	}
	if dep.ImageDigest != digest {
		t.Fatal("handoff retained an empty or different source identity")
	}
	task, err := store.CreateAppTask(ctx, state.CreateAppTaskParams{AccountID: acct.ID, AppID: app.ID, DeploymentID: dep.ID, Kind: state.AppTaskKindRelease, Command: dep.ReleaseCommand})
	if err != nil || task.ImageDigest != digest || task.ArtifactKey == "builder/source" {
		t.Fatalf("source release admission: digest=%q artifact=%q err=%v", task.ImageDigest, task.ArtifactKey, err)
	}
}

func TestLocalOCIManifestDigestRejectsContentMismatch(t *testing.T) {
	manifest := []byte(`{"schemaVersion":2}`)
	digest := digestFor(t, manifest)
	archive := buildLocalOCIArchive(t, map[string][]byte{"index.json": minimalIndexBytes(digest), blobPath(digest): append(manifest, ' ')})
	if _, err := localOCIManifestDigest(archive); err == nil {
		t.Fatal("accepted a manifest under a false content address")
	}
}
