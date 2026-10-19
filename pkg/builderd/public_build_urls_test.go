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

func TestPublicBuildURLsPartitionCachedArtifacts(t *testing.T) {
	c := NewCache(t.TempDir())
	p := filepath.Join(t.TempDir(), "image.tar")
	if err := os.WriteFile(p, []byte("frontend with old URL"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := testBuildCacheRecipe("source", FrameworkNode, api.PlanPro, "base")
	r.PublicBuildURLs = map[string]string{"VITE_API_URL": "https://old.example.com"}
	if err := c.StoreBuild(r, p, 21); err != nil {
		t.Fatal(err)
	}
	other := r
	other.PublicBuildURLs = map[string]string{"VITE_API_URL": "https://new.example.com"}
	if _, hit := c.LookupBuild(other); hit {
		t.Fatal("changed URL reused cached frontend")
	}
	other.PublicBuildURLs = nil
	if _, hit := c.LookupBuild(other); hit {
		t.Fatal("unconfigured frontend reused public build")
	}
	if _, hit := c.LookupBuild(r); !hit {
		t.Fatal("same public configuration missed cache")
	}
}

func TestPublicBuildURLsRequireGuestAcknowledgementAndUseCapturedValues(t *testing.T) {
	for _, acknowledged := range []bool{false, true} {
		t.Run(map[bool]string{false: "older guest rejected", true: "reviewed input accepted"}[acknowledged], func(t *testing.T) {
			store := state.NewMemStore()
			acct, _ := store.CreateAccount(t.Context(), "public-build@example.com", api.PlanPro)
			app, _ := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "public-build", RAMMB: 256, IdleTimeoutS: 60, MaxConcurrency: 5})
			urls := map[string]string{"VITE_API_URL": "https://reviewed.example.com/v1"}
			if err := store.SetAppPublicBuildURLs(t.Context(), acct.ID, app.ID, urls); err != nil {
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
			if err := store.SetAppPublicBuildURLs(t.Context(), acct.ID, app.ID, map[string]string{"VITE_API_URL": "https://changed.example.com"}); err != nil {
				t.Fatal(err)
			}
			artifact := filepath.Join(t.TempDir(), "image.tar")
			if err := os.WriteFile(artifact, []byte("frontend"), 0o600); err != nil {
				t.Fatal(err)
			}
			vm := &fakeVM{out: BuildOutcome{OCIImage: artifact}}
			if acknowledged {
				vm.out.PublicBuildURLsSHA256 = api.PublicBuildURLsSHA256(urls)
			}
			b := New(store, &fakeNotifier{}, vm, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			_, err = b.ProcessOne(t.Context(), build.ID)
			if (err == nil) != acknowledged {
				t.Fatalf("guest acknowledgement result: %v", err)
			}
			if vm.lastRequest.PublicBuildURLs["VITE_API_URL"] != urls["VITE_API_URL"] {
				t.Fatal("mutable app intent replaced captured deployment input")
			}
			after, _ := store.DeploymentByID(t.Context(), dep.ID)
			if !acknowledged && after.RootfsPath != "" {
				t.Fatal("unacknowledged artifact published")
			}
		})
	}
}

func TestPublicBuildURLReaderFailsClosedWithoutSnapshotSupport(t *testing.T) {
	app := state.App{Manifest: state.AppManifest{PublicBuildURLs: map[string]string{"VITE_API_URL": "https://api.example.com"}}}
	if _, err := deploymentPublicBuildURLs(t.Context(), struct{}{}, app, state.Deployment{}); err == nil {
		t.Fatal("missing captured-input support accepted mutable app values")
	}
}
