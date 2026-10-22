// adr: 20261019195000
package imaged

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/state"
)

// sourceBuildArchive returns a builder OCI export whose config runs
// /app/serve and, when imageCheck is set, declares that HEALTHCHECK test.
func sourceBuildArchive(t *testing.T, imageCheck []string) string {
	t.Helper()
	rawLayer := []byte("source-build-layer")
	config := minimalConfigBytes(rawLayer)
	var document map[string]any
	if err := json.Unmarshal(config, &document); err != nil {
		t.Fatal(err)
	}
	process := map[string]any{"Cmd": []string{"/app/serve"}}
	if imageCheck != nil {
		process["Healthcheck"] = map[string]any{"Test": imageCheck, "Interval": int64(30e9)}
	}
	document["config"] = process
	config, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	layer := gzipBytes(t, rawLayer)
	manifest := minimalManifestBytes(config, layer)
	return buildLocalOCIArchive(t, map[string][]byte{
		"index.json": minimalIndexBytes(manifest), blobPath(digestFor(t, manifest)): manifest,
		blobPath(digestFor(t, config)): config, blobPath(digestFor(t, layer)): layer,
	})
}

func TestSourceBuildAppliesComposeHealthcheck(t *testing.T) {
	check := &api.ComposeHealthcheck{Test: []string{"CMD", "/app/check"}, Retries: 2}
	freeze := func(check *api.ComposeHealthcheck) []byte {
		raw, err := json.Marshal(frameworkprofile.Profile{Version: frameworkprofile.Version, Framework: "docker",
			ImageHealthcheck: &frameworkprofile.ImageHealthcheck{Version: frameworkprofile.ImageHealthcheckVersion, Override: check}})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	captured := freeze(check)
	for _, tc := range []struct {
		name         string
		imageCheck   []string
		profile      []byte
		appCheck     *api.ComposeHealthcheck
		wantTest     []string
		wantRetries  int
		wantRequired bool
		wantCaptured bool
	}{
		{"frozen override", nil, captured, nil, []string{"CMD", "/app/check"}, 2, true, true},
		{"frozen override wins over the app's", nil, captured, &api.ComposeHealthcheck{Test: []string{"NONE"}}, []string{"CMD", "/app/check"}, 2, true, true},
		{"timing override on the image's own check", []string{"CMD", "/image"}, freeze(&api.ComposeHealthcheck{Retries: 5}), nil, []string{"CMD", "/image"}, 5, true, true},
		{"disabled override", []string{"CMD", "/image"}, freeze(&api.ComposeHealthcheck{Test: []string{"NONE"}}), nil, []string{"NONE"}, 0, false, true},
		{"an app override alone is not applied", []string{"CMD", "/image"}, nil, &api.ComposeHealthcheck{Retries: 5}, []string{"CMD", "/image"}, 0, false, false},
		{"no override keeps the image check unenforced", []string{"CMD", "/image"}, nil, nil, []string{"CMD", "/image"}, 0, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			acct, err := store.CreateAccount(ctx, "source-healthcheck@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "source-healthcheck", Type: state.AppTypeApp,
				Manifest: state.AppManifest{BuildDockerfile: "Dockerfile", ProjectImageHealthcheck: tc.appCheck}})
			if err != nil {
				t.Fatal(err)
			}
			dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindDockerfile, Status: state.DeployImaging, InferredProfile: tc.profile})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.SetDeploymentRootfs(ctx, dep.ID, sourceBuildArchive(t, tc.imageCheck), "builder/source", 1); err != nil {
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
			got := builder.calls[0].Manifest.Healthcheck
			if got == nil || !reflect.DeepEqual(got.Test, tc.wantTest) || got.Retries != tc.wantRetries {
				t.Fatalf("manifest healthcheck = %+v; want test %q retries %d", got, tc.wantTest, tc.wantRetries)
			}
			stored, err := store.DeploymentByID(ctx, dep.ID)
			if err != nil {
				t.Fatal(err)
			}
			frozen, err := frameworkprofile.ImageHealthcheckFromProfile(stored.InferredProfile)
			if err != nil || (frozen != nil) != tc.wantCaptured {
				t.Fatalf("frozen healthcheck = %+v (%v); want captured=%v in %s", frozen, err, tc.wantCaptured, stored.InferredProfile)
			}
			if required := frameworkprofile.RequiresSourceImageHealthcheck(stored.InferredProfile); required != tc.wantRequired {
				t.Fatalf("required = %v; want %v (profile %s)", required, tc.wantRequired, stored.InferredProfile)
			}
		})
	}
}
