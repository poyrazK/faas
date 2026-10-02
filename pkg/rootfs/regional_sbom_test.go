package rootfs

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/region"
	"github.com/onebox-faas/faas/pkg/storage"
)

func TestRegionalSBOMPublication(t *testing.T) {
	for _, full := range []bool{false, true} {
		for _, routed := range []bool{false, true} {
			name := "layer"
			if full {
				name = "full-rootfs"
			}
			if routed {
				name += "/routed"
			} else {
				name += "/unrouted"
			}
			t.Run(name, func(t *testing.T) {
				legacy, _ := storage.NewLocalStorageBackend(t.TempDir())
				eu, _ := storage.NewLocalStorageBackend(t.TempDir())
				var backend storage.StorageBackend = legacy
				if routed {
					backend, _ = storage.NewRegionRouter(map[string]storage.StorageBackend{"eu-sbom": eu}, legacy)
				}
				key := region.BuildSBOMKey("eu-sbom", "550e8400-e29b-41d4-a716-446655440000")
				body := []byte(`{"bomFormat":"CycloneDX","specVersion":"1.5","version":1}`)
				scan := func(context.Context, string) ([]byte, error) { return body, nil }
				b := &Builder{}
				var got string
				var err error
				if full {
					got, err = b.emitFullRootfsSBOM(t.Context(), BuildFullRootfsInput{Storage: backend, SBOMStorageKey: key, SBOMRun: scan}, t.TempDir())
				} else {
					got, err = b.emitSBOM(t.Context(), BuildInput{Storage: backend, SBOMStorageKey: key, SBOMRun: scan}, t.TempDir())
				}
				if err != nil {
					t.Fatal(err)
				}
				if routed {
					if got != key {
						t.Fatalf("published %q", got)
					}
					r, err := backend.Get(t.Context(), key)
					if err != nil {
						t.Fatal(err)
					}
					payload, err := io.ReadAll(r)
					_ = r.Close()
					if err != nil || string(payload) != string(body) {
						t.Fatalf("payload=%s err=%v", payload, err)
					}
				} else if got != "" {
					t.Fatalf("unrouted publication %q", got)
				}
				for _, k := range []string{key, strings.TrimPrefix(key, "regions/eu-sbom/")} {
					if r, err := legacy.Get(t.Context(), k); !errors.Is(err, storage.ErrNotFound) {
						if r != nil {
							_ = r.Close()
						}
						t.Fatalf("SBOM escaped to legacy key %q: %v", k, err)
					}
				}
			})
		}
	}
}
