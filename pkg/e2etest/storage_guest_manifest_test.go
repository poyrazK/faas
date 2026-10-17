package e2etest

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/e2etest/storagestarter"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestStorageGuestManifestRequiresHarnessArtifactsAndDeployedAppArtifacts(t *testing.T) {
	want := append(append([]string(nil), DaemonBinaries...), StaticHelperBinaries...)
	want = append(want, "guest_init", "kernel", "builder_base", "firecracker", "jailer", "e2e_test", "deploy_base")
	contract := storagestarter.SuiteContract()
	if len(want) != len(contract.Artifacts) {
		t.Fatal("storage acceptance contract omits a harness artifact")
	}
	seen := map[string]bool{}
	for _, name := range contract.Artifacts {
		seen[name] = true
	}
	for _, name := range want {
		if !seen[name] {
			t.Fatalf("storage acceptance contract omits %s", name)
		}
	}
	if _, err := StorageGuestRuntimeManifest(map[string]string{"foreign": "unused"}, "a", "object-storage-node", false); err == nil {
		t.Fatal("unknown storage runtime artifact accepted")
	}
	if _, err := StorageGuestRuntimeManifest(nil, "a", "object-storage-node", false); err == nil {
		t.Fatal("missing native binaries accepted")
	}
	path := filepath.Join(t.TempDir(), "apid")
	if err := os.WriteFile(path, []byte("not a stamped executable"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := StorageGuestRuntimeManifest(map[string]string{"apid": path}, "a", "object-storage-node", false); err == nil {
		t.Fatal("unverified cached acceptance executable accepted")
	}
}

func TestStorageGuestAppArtifactsRequireExactSourceBuildAndFullOCIApp(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.tar.gz")
	if err := os.WriteFile(source, []byte("owned exact source archive"), 0600); err != nil {
		t.Fatal(err)
	}
	digest, err := PostgresGuestArtifactDigest(source)
	if err != nil {
		t.Fatal(err)
	}
	type records struct {
		app        state.App
		deployment state.Deployment
		build      state.Build
		provenance state.BuildProvenance
	}
	valid := records{
		app: state.App{ID: "app", Slug: "storage", Type: state.AppTypeApp},
		deployment: state.Deployment{ID: "deployment", AppID: "app", RootfsKey: sched.AppLayerKey("storage", "deployment"),
			RootfsPath: filepath.Join(root, "application.ext4"), SourceSHA256: digest, ImageDigest: "sha256:" + strings.Repeat("b", 64)},
		build:      state.Build{ID: "build", DeploymentID: "deployment", Status: state.BuildSucceeded},
		provenance: state.BuildProvenance{BuildID: "build", SourceSHA256: digest},
	}
	cases := []struct {
		name    string
		corrupt func(*records)
	}{
		{"function", func(r *records) { r.app.Type = state.AppTypeFunction }},
		{"function-runtime", func(r *records) { r.app.Runtime = "node22" }},
		{"foreign-app", func(r *records) { r.deployment.AppID = "foreign" }},
		{"foreign-layer", func(r *records) { r.deployment.RootfsKey = "apps/other/foreign.ext4" }},
		{"missing-image-pin", func(r *records) { r.deployment.ImageDigest = "" }},
		{"invalid-image-pin", func(r *records) { r.deployment.ImageDigest = "sha256:" + strings.Repeat("x", 64) }},
		{"unfinished-build", func(r *records) { r.build.Status = state.BuildRunning }},
		{"foreign-build", func(r *records) { r.build.DeploymentID = "foreign" }},
		{"foreign-provenance", func(r *records) { r.provenance.BuildID = "foreign" }},
		{"different-deployment-source", func(r *records) { r.deployment.SourceSHA256 = strings.Repeat("c", 64) }},
		{"different-builder-source", func(r *records) { r.provenance.SourceSHA256 = strings.Repeat("c", 64) }},
	}
	for _, starter := range storagestarter.SuiteContract().Starters {
		t.Run(starter.Template, func(t *testing.T) {
			call := func(r records) (map[string]string, error) {
				return StorageGuestAppArtifacts(starter.Template, root, source, r.app, r.deployment, r.build, r.provenance)
			}
			paths, err := call(valid)
			want := map[string]string{"deploy_base": filepath.Join(root, "base/base-amd64.ext4"),
				starter.AppArtifact: valid.deployment.RootfsPath, starter.SourceArtifact: source}
			if err != nil || !reflect.DeepEqual(paths, want) {
				t.Fatal("full OCI application's actual source, app layer and minimal base were not selected")
			}
			for _, test := range cases {
				t.Run(test.name, func(t *testing.T) {
					changed := valid
					test.corrupt(&changed)
					if _, err := call(changed); err == nil {
						t.Fatal("unverified deployment/build evidence accepted")
					}
				})
			}
		})
	}
	if err := os.WriteFile(source, []byte("changed archive"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := StorageGuestAppArtifacts("object-storage-python", root, source, valid.app, valid.deployment, valid.build, valid.provenance); err == nil {
		t.Fatal("changed exact source bytes accepted")
	}
}

func TestStorageGuestManifestRejectsDifferentStartersLayer(t *testing.T) {
	for _, starter := range storagestarter.SuiteContract().Starters {
		for _, other := range storagestarter.SuiteContract().Starters {
			if other.Template == starter.Template {
				continue
			}
			// Even the pre-deploy manifest cannot accept another starter's application layer.
			_, err := StorageGuestRuntimeManifest(map[string]string{other.AppArtifact: "unused"}, "a", starter.Template, false)
			if err == nil || err.Error() != "unexpected storage acceptance artifact" {
				t.Fatalf("%s accepted %s application layer", starter.Template, other.Template)
			}
		}
	}
	if _, err := StorageGuestRuntimeManifest(nil, "a", "unknown", false); err == nil {
		t.Fatal("unsupported storage template accepted")
	}
}
