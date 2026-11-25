package imaged

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261116192126 — a cache deployment may run any accepted Valkey image:
// the one apid deploys, or an older one an existing cache keeps until
// `gregale redis update` (a newer one is accepted a release before apid
// deploys it). Any other image on a cache app is refused, and apps that are
// not caches are not checked.
func TestManagedValkeyImageCheckAcceptsEveryCacheImage(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	h := New(store, nil, nil, nil, "", t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	cache, err := store.CreateApp(ctx, state.App{AccountID: "acct-1", Slug: "sessions"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateManagedValkeyCache(ctx, state.ManagedValkeyCache{AppID: cache.ID, AccountID: "acct-1", MemoryMB: 128}); err != nil {
		t.Fatal(err)
	}
	web, err := store.CreateApp(ctx, state.App{AccountID: "acct-1", Slug: "web"})
	if err != nil {
		t.Fatal(err)
	}
	for _, image := range api.ManagedValkeyImages {
		if err := h.managedValkeyImageCheck(ctx, cache, image); err != nil {
			t.Errorf("cache image %s refused: %v", image, err)
		}
	}
	other := "docker.io/library/redis@sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := h.managedValkeyImageCheck(ctx, cache, other); !errors.Is(err, errManagedValkeyImage) {
		t.Fatalf("other image on a cache: err = %v, want errManagedValkeyImage", err)
	}
	if err := h.managedValkeyImageCheck(ctx, web, other); err != nil {
		t.Fatalf("app that is not a cache: %v", err)
	}
}
