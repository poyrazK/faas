package e2etest

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres/guestqualification"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresGuestManifestCoversHarnessAndRuntimeArtifacts(t *testing.T) {
	names := append(append([]string(nil), DaemonBinaries...), StaticHelperBinaries...)
	names = append(names, "guest_init", "kernel", "builder_base", "firecracker", "jailer", "e2e_test", "deploy_base", "probe_app_layer", "probe_source_archive", "node22_app_layer", "node22_source_archive", "python312_app_layer", "python312_source_archive")
	contract := guestqualification.SuiteContract()
	if len(names) != len(contract.Artifacts) {
		t.Fatal("guest contract omits a harness artifact")
	}
	seen := make(map[string]bool)
	for _, name := range contract.Artifacts {
		seen[name] = true
	}
	for _, name := range names {
		if !seen[name] {
			t.Fatalf("guest contract omits %s", name)
		}
	}
	path := filepath.Join(t.TempDir(), "base")
	if err := os.WriteFile(path, []byte("first base"), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := PostgresGuestArtifactDigest(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement base"), 0600); err != nil {
		t.Fatal(err)
	}
	second, err := PostgresGuestArtifactDigest(path)
	if err != nil || first == second {
		t.Fatal("base replacement not distinguished")
	}
	if _, err := PostgresGuestArtifactDigest(filepath.Dir(path)); err == nil {
		t.Fatal("directory accepted as runtime artifact")
	}
}

func TestPostgresGuestAppArtifactsUseFullOCIProvenance(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.tar.gz")
	if err := os.WriteFile(source, []byte("owned source archive"), 0600); err != nil {
		t.Fatal(err)
	}
	digest, err := PostgresGuestArtifactDigest(source)
	if err != nil {
		t.Fatal(err)
	}
	app := state.App{ID: "app", Slug: "postgres", Type: state.AppTypeApp}
	deployment := state.Deployment{ID: "deployment", AppID: app.ID, RootfsPath: filepath.Join(root, "app.ext4"),
		RootfsKey: sched.AppLayerKey(app.Slug, "deployment"), SourceSHA256: digest, ImageDigest: "sha256:" + strings.Repeat("b", 64)}
	build := state.Build{ID: "build", DeploymentID: deployment.ID, Status: state.BuildSucceeded}
	provenance := state.BuildProvenance{BuildID: build.ID, SourceSHA256: digest}
	for _, workload := range []string{"probe", "node22", "python312"} {
		t.Run(workload, func(t *testing.T) {
			paths, err := PostgresGuestAppArtifacts(workload, root, source, app, deployment, build, provenance)
			want := map[string]string{"deploy_base": filepath.Join(root, "base/base-amd64.ext4"),
				workload + "_app_layer": deployment.RootfsPath, workload + "_source_archive": source}
			if err != nil || !reflect.DeepEqual(paths, want) {
				t.Fatal("full OCI artifacts were not selected")
			}
			foreign := provenance
			foreign.BuildID = "foreign"
			if _, err := PostgresGuestAppArtifacts(workload, root, source, app, deployment, build, foreign); err == nil {
				t.Fatal("another build's provenance accepted")
			}
		})
	}
	if _, err := PostgresGuestAppArtifacts("python313", root, source, app, deployment, build, provenance); err == nil {
		t.Fatal("unqualified workload accepted")
	}
	if err := os.WriteFile(source, []byte("changed archive"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := PostgresGuestAppArtifacts("probe", root, source, app, deployment, build, provenance); err == nil {
		t.Fatal("source archive changed after upload")
	}
	for _, name := range []string{"probe_base", "node22_base", "foreign"} {
		if _, err := PostgresGuestRuntimeManifest(map[string]string{name: "unused"}, strings.Repeat("a", 40), false); err == nil {
			t.Fatal("legacy or unexpected runtime artifact accepted")
		}
	}
}

func TestPostgresGuestCachedBinaryMustMatchCleanNativeRevision(t *testing.T) {
	revision := strings.Repeat("a", 40)
	settings := map[string]string{"vcs": "git", "vcs.revision": revision, "vcs.modified": "false", "GOOS": "linux", "GOARCH": "amd64"}
	build := func(values map[string]string) *debug.BuildInfo {
		b := &debug.BuildInfo{}
		for k, v := range values {
			b.Settings = append(b.Settings, debug.BuildSetting{Key: k, Value: v})
		}
		return b
	}
	if !postgresGuestBinaryRevision(build(settings), revision) {
		t.Fatal("valid native binary rejected")
	}
	for _, key := range []string{"vcs.revision", "vcs.modified", "GOOS", "GOARCH"} {
		t.Run(key, func(t *testing.T) {
			candidate := make(map[string]string)
			for k, v := range settings {
				candidate[k] = v
			}
			candidate[key] = "wrong"
			if postgresGuestBinaryRevision(build(candidate), revision) {
				t.Fatal("foreign/dirty cached binary accepted")
			}
		})
	}
	if postgresGuestBinaryRevision(&debug.BuildInfo{}, revision) {
		t.Fatal("unstamped cached binary accepted")
	}
}
