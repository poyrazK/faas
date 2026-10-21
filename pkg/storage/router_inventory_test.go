// adr: 054
package storage

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestPrefixRouterListMatchesReadableNamespace(t *testing.T) {
	apps, special, nested, fallback := newFakeOCI(), newFakeOCI(), newFakeOCI(), newFakeOCI()
	for _, key := range []string{"public/a", "special/shadow-only", "special/live"} {
		apps.blobs[key] = []byte(key)
	}
	for _, key := range []string{"live", "inside/shadow-only"} {
		special.blobs[key] = []byte(key)
	}
	nested.blobs["leaf"] = []byte("leaf")
	for _, key := range []string{"misc/blob", "apps/shadow-only", "apps/special/live", "apps-extra/blob"} {
		fallback.blobs[key] = []byte(key)
	}
	router, err := NewPrefixRouter(map[string]StorageBackend{
		"apps/": apps, "apps/special/": special, "apps/special/inside/": nested,
	}, fallback)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		prefix string
		want   []string
	}{
		{"", []string{"apps-extra/blob", "apps/public/a", "apps/special/inside/leaf", "apps/special/live", "misc/blob"}},
		{"apps", []string{"apps-extra/blob", "apps/public/a", "apps/special/inside/leaf", "apps/special/live"}},
		{"apps/", []string{"apps/public/a", "apps/special/inside/leaf", "apps/special/live"}},
		{"apps/s", []string{"apps/special/inside/leaf", "apps/special/live"}},
		{"apps/special/", []string{"apps/special/inside/leaf", "apps/special/live"}},
		{"apps/special/inside/l", []string{"apps/special/inside/leaf"}},
		{"misc/", []string{"misc/blob"}},
	} {
		t.Run("prefix="+tc.prefix, func(t *testing.T) {
			keys, err := router.List(t.Context(), tc.prefix)
			if err != nil || !slices.Equal(keys, tc.want) {
				t.Errorf("List(%q) = %v, %v; want %v", tc.prefix, keys, err, tc.want)
			}
			for _, key := range keys {
				reader, err := router.Get(t.Context(), key)
				if err != nil {
					t.Errorf("listed key %q cannot be read through its owning route: %v", key, err)
					continue
				}
				_ = reader.Close()
			}
		})
	}
}

func TestPrefixRouterScopedListSkipsShadowedBackends(t *testing.T) {
	unused := &unavailableInventoryBackend{fakeOCIBackend: newFakeOCI()}
	owner := newFakeOCI()
	owner.blobs["live"] = []byte("value")
	router, err := NewPrefixRouter(map[string]StorageBackend{"apps/": unused, "apps/special/": owner}, unused)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := router.List(t.Context(), "apps/special/")
	if err != nil || !slices.Equal(keys, []string{"apps/special/live"}) {
		t.Fatalf("scoped List = %v, %v; must only depend on the owning backend", keys, err)
	}
}

type unavailableInventoryBackend struct{ *fakeOCIBackend }

func (*unavailableInventoryBackend) List(context.Context, string) ([]string, error) {
	return nil, errors.New("unrelated backend unavailable")
}
