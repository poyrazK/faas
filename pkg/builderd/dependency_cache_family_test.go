package builderd

import (
	"os"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// A4: a new app with no dependency cache starts from its account's family
// cache; other accounts and frameworks never share one.
func TestDependencyCacheFamilySeedsNewApps(t *testing.T) {
	drive := t.TempDir()
	cart := state.App{ID: "cart", AccountID: "acct-1"}
	orders := state.App{ID: "orders", AccountID: "acct-1"}
	family := dependencyCacheFamilyKey(cart, "docker", "base@sha256:1")
	if family == "" || family != dependencyCacheFamilyKey(orders, "docker", "base@sha256:1") {
		t.Fatal("apps of one account and framework must share a family")
	}
	if family == dependencyCacheFamilyKey(state.App{ID: "x", AccountID: "acct-2"}, "docker", "base@sha256:1") ||
		family == dependencyCacheFamilyKey(cart, "node", "base@sha256:1") {
		t.Fatal("a family must not span accounts or frameworks")
	}

	cartKey := dependencyCacheKeyForApp(cart, "docker", "", "base@sha256:1")
	ordersKey := dependencyCacheKeyForApp(orders, "docker", "", "base@sha256:1")
	cartPath, _ := dependencyCachePath(drive, cartKey)
	ordersPath, _ := dependencyCachePath(drive, ordersKey)

	if got := dependencyCacheStagePath(drive, ordersPath, family, ordersKey); got != ordersPath {
		t.Fatalf("no family cache yet: stage %q, want own %q", got, ordersPath)
	}
	if err := os.MkdirAll(cartPath, 0o700); err != nil {
		t.Fatal(err)
	}
	recordDependencyCacheFamily(drive, family, cartKey)
	if got := dependencyCacheStagePath(drive, ordersPath, family, ordersKey); got != cartPath {
		t.Fatalf("new app stages %q, want the family's %q", got, cartPath)
	}
	if got := dependencyCacheStagePath(drive, ordersPath, "", ordersKey); got != ordersPath {
		t.Fatalf("a build without a family (secrets) stages %q, want own", got)
	}
	if err := os.MkdirAll(ordersPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := dependencyCacheStagePath(drive, ordersPath, family, ordersKey); got != ordersPath {
		t.Fatalf("an app with its own cache stages %q, want own", got)
	}
}
