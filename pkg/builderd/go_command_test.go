package builderd

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261025134512: a service of a multi-command Go module builds its own
// cmd/ main package, or fails; it never builds the module's default command.
func TestSelectGoCommand(t *testing.T) {
	app := func(command string) state.App {
		return state.App{Manifest: state.AppManifest{BuildGoCommand: command}}
	}
	for _, tc := range []struct {
		name       string
		app        state.App
		dockerfile string
		fw         Framework
		want       string
		wantErr    bool
	}{
		{"go build", app("worker"), "", FrameworkGo, "worker", false},
		{"no selection", app(""), "", FrameworkGo, "", false},
		{"no selection, Dockerfile", app(""), "Dockerfile", FrameworkDocker, "", false},
		{"Dockerfile cannot honour it", app("worker"), "Dockerfile", FrameworkDocker, "", true},
		{"other language cannot honour it", app("worker"), "", FrameworkNode, "", true},
		{"invalid name", app("../worker"), "", FrameworkGo, "", true},
	} {
		got, err := selectGoCommand(tc.app, tc.dockerfile, tc.fw)
		if got != tc.want || (err != nil) != tc.wantErr {
			t.Errorf("%s: command = %q, err = %v; want %q, error %t", tc.name, got, err, tc.want, tc.wantErr)
		}
	}
}

// Two commands of one module share source, root and framework; the recipe
// must still tell them apart, or one would be served the other's binary.
func TestBuildCacheRecipeSeparatesGoCommands(t *testing.T) {
	recipe := BuildCacheRecipe{SourceSHA256: "abc", SourceRoot: ".", Framework: FrameworkGo, Plan: api.PlanPro,
		RuntimeBaseRef: "base", BuilderBaseIdentity: "builder", TargetPlatform: "linux/amd64", AccountID: "acct"}
	plain, err := recipe.key()
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{plain: true}
	for _, command := range []string{"api", "worker"} {
		recipe.GoCommand = command
		key, err := recipe.key()
		if err != nil {
			t.Fatal(err)
		}
		if keys[key] {
			t.Fatalf("cmd/%s shares a recipe key with another build", command)
		}
		keys[key] = true
	}
}

func TestGoCommandBuildRequiresGuestAcknowledgement(t *testing.T) {
	for _, acknowledged := range []bool{false, true} {
		t.Run(map[bool]string{false: "older guest rejected", true: "acknowledged command accepted"}[acknowledged], func(t *testing.T) {
			store := state.NewMemStore()
			acct, _ := store.CreateAccount(t.Context(), "go-command@example.com", api.PlanPro)
			app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "shop-worker", RAMMB: 256, IdleTimeoutS: 60, MaxConcurrency: 5,
				Manifest: state.AppManifest{BuildGoCommand: "worker"}})
			if err != nil {
				t.Fatal(err)
			}
			src := filepath.Join(t.TempDir(), "src.tar.gz")
			makeTarballWithName(t, src, []string{"go.mod", "cmd/api/main.go", "cmd/worker/main.go"})
			dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, SourcePath: src,
				LogPath: filepath.Join(t.TempDir(), "build.log")})
			if err != nil {
				t.Fatal(err)
			}
			build, err := store.CreateBuild(t.Context(), dep.ID, state.DeploymentKindTarball, 100, dep.LogPath)
			if err != nil {
				t.Fatal(err)
			}
			artifact := filepath.Join(t.TempDir(), "image.tar")
			if err := os.WriteFile(artifact, []byte("worker"), 0o600); err != nil {
				t.Fatal(err)
			}
			vm := &fakeVM{out: BuildOutcome{OCIImage: artifact}}
			if acknowledged {
				vm.out.GoCommand = "worker"
			}
			b := New(store, &fakeNotifier{}, vm, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			_, err = b.ProcessOne(t.Context(), build.ID)
			if (err == nil) != acknowledged {
				t.Fatalf("guest acknowledgement result: %v", err)
			}
			if vm.lastRequest.GoCommand != "worker" {
				t.Fatalf("builder received go command %q, want worker", vm.lastRequest.GoCommand)
			}
			after, _ := store.DeploymentByID(t.Context(), dep.ID)
			if !acknowledged && after.RootfsPath != "" {
				t.Fatal("unacknowledged artifact published")
			}
		})
	}
}
