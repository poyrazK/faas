package builderd

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestBuildCacheRecipeSeparatesDockerfileBuildOptions(t *testing.T) {
	base := testBuildCacheRecipe("source", FrameworkDocker, api.PlanPro, "")
	plain, err := base.key()
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]string{"plain": plain}
	for name, options := range map[string]*api.DockerfileBuildOptions{
		"target":      {Target: "runtime"},
		"other stage": {Target: "test"},
		"args":        {Args: map[string]string{"NODE_ENV": "production"}},
		"other value": {Args: map[string]string{"NODE_ENV": "development"}},
	} {
		recipe := base
		recipe.DockerfileBuild = options
		key, err := recipe.key()
		if err != nil {
			t.Fatal(err)
		}
		for other, otherKey := range keys {
			if key == otherKey {
				t.Fatalf("%s shares a cache key with %s", name, other)
			}
		}
		keys[name] = key
	}
	if key, _ := base.key(); key != plain {
		t.Fatal("a recipe without options changed its key")
	}
}

func TestSelectDockerfileBuild(t *testing.T) {
	current := &api.DockerfileBuildOptions{Target: "current"}
	captured := &api.DockerfileBuildOptions{Target: "captured", Args: map[string]string{"A": "1"}}
	profile, err := json.Marshal(frameworkprofile.Profile{Version: frameworkprofile.Version, Framework: "docker", DockerfileBuild: captured})
	if err != nil {
		t.Fatal(err)
	}
	app := state.App{Manifest: state.AppManifest{DockerfileBuild: current}}
	withProfile := state.Deployment{InferredProfile: profile}
	for _, tc := range []struct {
		name     string
		app      state.App
		dep      state.Deployment
		reviewed bool
		fw       Framework
		want     *api.DockerfileBuildOptions
		ignored  bool
	}{
		{"captured options win", app, withProfile, false, FrameworkDocker, captured, false},
		{"app options without a captured profile", app, state.Deployment{}, false, FrameworkDocker, current, false},
		{"reviewed source selection carries none", app, withProfile, true, FrameworkDocker, nil, false},
		{"railpack build ignores them", app, withProfile, false, FrameworkNode, nil, true},
		{"no options", state.App{}, state.Deployment{}, false, FrameworkDocker, nil, false},
	} {
		got, ignored, err := selectDockerfileBuild(tc.app, tc.dep, tc.reviewed, tc.fw)
		if err != nil || ignored != tc.ignored || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: options=%+v ignored=%t err=%v; want %+v ignored=%t", tc.name, got, ignored, err, tc.want, tc.ignored)
		}
	}
	corrupt := state.App{Manifest: state.AppManifest{DockerfileBuild: &api.DockerfileBuildOptions{Args: map[string]string{"A=B": "x"}}}}
	if _, _, err := selectDockerfileBuild(corrupt, state.Deployment{}, false, FrameworkDocker); err == nil {
		t.Fatal("an invalid stored argument name reached the builder")
	}
}

func TestShadowedBuildArgs(t *testing.T) {
	options := &api.DockerfileBuildOptions{Args: map[string]string{"VITE_API_URL": "x", "NODE_ENV": "production", "API_URL": "y"}}
	got := shadowedBuildArgs(options, map[string]string{"VITE_API_URL": "https://a", "API_URL": "https://b", "OTHER_URL": "https://c"})
	if want := []string{"API_URL", "VITE_API_URL"}; !slices.Equal(got, want) {
		t.Fatalf("shadowed = %q, want %q", got, want)
	}
	if got := shadowedBuildArgs(nil, map[string]string{"A_URL": "https://a"}); got != nil {
		t.Fatalf("nil options shadowed %q", got)
	}
}

func TestDockerfileBuildRequiresGuestAcknowledgementAndUsesCapturedOptions(t *testing.T) {
	captured := &api.DockerfileBuildOptions{Target: "runtime", Args: map[string]string{"NODE_ENV": "production"}}
	for _, acknowledged := range []bool{false, true} {
		t.Run(map[bool]string{false: "older guest rejected", true: "acknowledged options accepted"}[acknowledged], func(t *testing.T) {
			store := state.NewMemStore()
			acct, _ := store.CreateAccount(t.Context(), "dockerfile-build@example.com", api.PlanPro)
			// The app has moved on since the deployment was enqueued.
			app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "dockerfile-build", RAMMB: 256, IdleTimeoutS: 60, MaxConcurrency: 5,
				Manifest: state.AppManifest{BuildDockerfile: "Dockerfile", DockerfileBuild: &api.DockerfileBuildOptions{Target: "test"}}})
			if err != nil {
				t.Fatal(err)
			}
			profile, err := json.Marshal(frameworkprofile.Profile{Version: frameworkprofile.Version, Framework: "docker", DockerfilePath: "Dockerfile", DockerfileBuild: captured})
			if err != nil {
				t.Fatal(err)
			}
			src := filepath.Join(t.TempDir(), "src.tar.gz")
			makeTarballWithName(t, src, []string{"Dockerfile", "server.js"})
			dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindDockerfile, SourcePath: src,
				InferredProfile: profile, LogPath: filepath.Join(t.TempDir(), "build.log")})
			if err != nil {
				t.Fatal(err)
			}
			build, err := store.CreateBuild(t.Context(), dep.ID, state.DeploymentKindDockerfile, 100, dep.LogPath)
			if err != nil {
				t.Fatal(err)
			}
			artifact := filepath.Join(t.TempDir(), "image.tar")
			if err := os.WriteFile(artifact, []byte("runtime stage"), 0o600); err != nil {
				t.Fatal(err)
			}
			vm := &fakeVM{out: BuildOutcome{OCIImage: artifact}}
			if acknowledged {
				vm.out.DockerfileBuildSHA256 = captured.SHA256()
			}
			b := New(store, &fakeNotifier{}, vm, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			_, err = b.ProcessOne(t.Context(), build.ID)
			if (err == nil) != acknowledged {
				t.Fatalf("guest acknowledgement result: %v", err)
			}
			if !reflect.DeepEqual(vm.lastRequest.DockerfileBuild, captured) {
				t.Fatalf("builder received %+v; want the captured %+v", vm.lastRequest.DockerfileBuild, captured)
			}
			after, _ := store.DeploymentByID(t.Context(), dep.ID)
			if !acknowledged && after.RootfsPath != "" {
				t.Fatal("unacknowledged artifact published")
			}
		})
	}
}

func TestDockerfileBuildOptionsSkipRailpackBuilds(t *testing.T) {
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(t.Context(), "railpack-build@example.com", api.PlanPro)
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "railpack-build", RAMMB: 256, IdleTimeoutS: 60, MaxConcurrency: 5,
		Manifest: state.AppManifest{DockerfileBuild: &api.DockerfileBuildOptions{Target: "runtime"}}})
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "src.tar.gz")
	makeTarballWithName(t, src, []string{"package.json", "index.js"})
	dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, SourcePath: src, LogPath: filepath.Join(t.TempDir(), "build.log")})
	if err != nil {
		t.Fatal(err)
	}
	build, err := store.CreateBuild(t.Context(), dep.ID, state.DeploymentKindTarball, 100, dep.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(t.TempDir(), "image.tar")
	if err := os.WriteFile(artifact, []byte("railpack"), 0o600); err != nil {
		t.Fatal(err)
	}
	vm := &fakeVM{out: BuildOutcome{OCIImage: artifact}}
	b := New(store, &fakeNotifier{}, vm, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := b.ProcessOne(t.Context(), build.ID); err != nil {
		t.Fatalf("Railpack build without an options acknowledgement: %v", err)
	}
	if vm.lastRequest.DockerfileBuild != nil {
		t.Fatalf("Railpack build received %+v", vm.lastRequest.DockerfileBuild)
	}
}
