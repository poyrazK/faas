package builderd

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 970 — only developer apps build watch-mode images.
func TestDevWatchCommandOnlyForDeveloperApps(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "watch@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	mk := func(slug, previewOf string, pr int, typ state.AppType) state.App {
		app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug, Type: typ, RAMMB: 512, MaxConcurrency: 1, IdleTimeoutS: 60,
			PreviewOfSlug: previewOf, PreviewPrNumber: pr})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetDevWatchCommand(ctx, app.ID, "npm run dev"); err != nil {
			t.Fatal(err)
		}
		return app
	}
	for _, tc := range []struct {
		app  state.App
		want string
	}{
		{mk("watch-dev", "api", 0, state.AppTypeApp), "npm run dev"},
		{mk("watch-prod", "", 0, state.AppTypeApp), ""},
		{mk("watch-pr", "api", 7, state.AppTypeApp), ""},
		{mk("watch-fn", "api", 0, state.AppTypeFunction), ""},
	} {
		got, err := devWatchCommand(ctx, store, tc.app)
		if err != nil || got != tc.want {
			t.Fatalf("%s: watch command = %q, %v; want %q", tc.app.Slug, got, err, tc.want)
		}
	}
}

func TestBuildCacheRecipeSeparatesWatchImages(t *testing.T) {
	base := BuildCacheRecipe{SourceSHA256: "abc", Framework: FrameworkNode, Plan: api.PlanPro, RuntimeBaseRef: "ref", BuilderBaseIdentity: "builder", TargetPlatform: "linux/amd64"}
	plain, err := base.key()
	if err != nil {
		t.Fatal(err)
	}
	watched := base
	watched.DevWatchCommand = "npm run dev"
	watchKey, err := watched.key()
	if err != nil {
		t.Fatal(err)
	}
	if plain == watchKey {
		t.Fatal("a watch-mode build shares the production build's cache key")
	}
	// An empty command must not change existing keys.
	again := base
	again.DevWatchCommand = ""
	if k, _ := again.key(); k != plain {
		t.Fatal("an empty watch command changed the recipe key")
	}
}
