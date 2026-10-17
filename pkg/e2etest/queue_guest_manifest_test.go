package e2etest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/e2etest/queuestarter"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/neon"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestQueueGuestCatalogNeedsNoProviderOperations(t *testing.T) {
	config := QueueGuestCatalogConfig()
	registry, err := managedpostgres.NewRegistry(config, func(string) string { return "unused-native-fixture" }, map[string]managedpostgres.Factory{"neon": neon.New})
	if err != nil || registry == nil || registry.ProvisioningEnabled || registry.UsagePolicy().Enabled || registry.HealthPolicy().Enabled {
		t.Fatalf("queue inventory catalog is not isolated and disabled: %v", err)
	}
}

func TestQueueGuestFunctionArtifactsRequireExactBuildAndPublishedRuntime(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.tar.gz")
	if err := os.WriteFile(source, []byte("exact owned queue source"), 0600); err != nil {
		t.Fatal(err)
	}
	digest, err := PostgresGuestArtifactDigest(source)
	if err != nil {
		t.Fatal(err)
	}
	app := state.App{ID: "app", Slug: "queue-native", Type: state.AppTypeFunction, Runtime: "node22"}
	dep := state.Deployment{ID: "deployment", AppID: app.ID, RootfsKey: sched.AppLayerKey(app.Slug, "deployment"), RootfsPath: filepath.Join(root, "layer.ext4"), SourceSHA256: digest, ImageDigest: "sha256:" + strings.Repeat("a", 64)}
	build := state.Build{ID: "build", DeploymentID: dep.ID, Status: state.BuildSucceeded}
	provenance := state.BuildProvenance{BuildID: build.ID, SourceSHA256: digest}
	release := state.RuntimeRelease{Runtime: "node22", Architecture: "amd64", SourceRef: "mirror.invalid/node@sha256:" + strings.Repeat("b", 64), GuestInitSHA256: strings.Repeat("c", 64), LayoutVersion: "v1", BaseSHA256: strings.Repeat("d", 64)}
	release.ID = release.Identity()
	provenance.RuntimeBaseRef = release.SourceRef
	paths, err := QueueGuestAppArtifacts(root, source, app, dep, build, provenance, release)
	if err != nil || paths["node22_base"] != filepath.Join(root, release.BaseKey()) || paths["app_layer"] != dep.RootfsPath || paths["source_archive"] != source {
		t.Fatalf("actual function artifacts: %v", err)
	}
	for _, name := range []string{"full OCI", "runtime", "app owner", "layer key", "unbuilt", "wrong build", "source", "image", "release", "architecture"} {
		t.Run(name, func(t *testing.T) {
			a, d, b, p, r := app, dep, build, provenance, release
			switch name {
			case "full OCI":
				a.Type = state.AppTypeApp
			case "runtime":
				a.Runtime = "python312"
			case "app owner":
				d.AppID = "foreign"
			case "layer key":
				d.RootfsKey = "foreign"
			case "unbuilt":
				b.Status = state.BuildFailed
			case "wrong build":
				p.BuildID = "other"
			case "source":
				p.SourceSHA256 = strings.Repeat("f", 64)
			case "image":
				d.ImageDigest = "sha256:invalid"
			case "release":
				r.ID = "foreign"
			case "architecture":
				r.Architecture = "arm64"
				r.ID = r.Identity()
			}
			if _, err := QueueGuestAppArtifacts(root, source, a, d, b, p, r); err == nil {
				t.Fatal("invalid function provenance accepted")
			}
		})
	}
}

func TestQueueGuestManifestRejectsMissingForeignAndUnstampedArtifacts(t *testing.T) {
	for _, paths := range []map[string]string{{}, {"foreign": "/unused"}, {"apid": "/bin/sh"}} {
		if _, err := QueueGuestRuntimeManifest(paths, strings.Repeat("a", 40), true); err == nil {
			t.Fatal("invalid queue execution bundle accepted")
		}
	}
	contract := queuestarter.SuiteContract()
	want := append(append([]string(nil), DaemonBinaries...), StaticHelperBinaries...)
	want = append(want, "guest_init", "kernel", "builder_base", "firecracker", "jailer", "e2e_test", "node22_base", "initial_app_layer", "candidate_app_layer", "source_archive")
	seen := map[string]bool{}
	for _, name := range contract.Artifacts {
		seen[name] = true
	}
	if len(contract.Artifacts) != len(want) || len(seen) != len(want) {
		t.Fatal("queue contract has missing or duplicate execution artifacts")
	}
	for _, name := range want {
		if !seen[name] {
			t.Fatalf("queue contract omits %s", name)
		}
	}
	found := false
	for _, name := range contract.Artifacts {
		found = found || name == "node22_base"
		if name == "deploy_base" {
			t.Fatal("function acceptance substituted minimal OCI base")
		}
	}
	if !found {
		t.Fatal("function runtime base missing")
	}
}
