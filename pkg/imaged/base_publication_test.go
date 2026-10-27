package imaged

// adr: 20261024191249 — the rollout pre-stage and vmmd base-adoption-check
// judge a runtime base the way vmmd's ensureBaseGeneration will.
// adr: 20261026181418 — the generation's own keys first, then the legacy
// logical key.

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/storagekey"
	"github.com/onebox-faas/faas/pkg/wire"
)

const (
	adoptionKey    = "base/base-amd64.ext4"
	adoptionGenOld = "registry.gregale.dev/base-minimal@sha256:5ca69ea00000000000000000000000000000000000000000000000000000000a"
	adoptionGenNew = "registry.gregale.dev/base-minimal@sha256:f615dc4f0000000000000000000000000000000000000000000000000000000b"
)

// sharedParent stands in for the shared registry: no local paths, and an
// optional failure for every read.
type sharedParent struct {
	b       storage.StorageBackend
	readErr error
}

func (p sharedParent) Put(ctx context.Context, key string, r io.Reader) error {
	return p.b.Put(ctx, key, r)
}
func (p sharedParent) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if p.readErr != nil {
		return nil, p.readErr
	}
	return p.b.Get(ctx, key)
}
func (p sharedParent) Delete(ctx context.Context, key string) error { return p.b.Delete(ctx, key) }
func (p sharedParent) List(ctx context.Context, prefix string) ([]string, error) {
	if p.readErr != nil {
		return nil, p.readErr
	}
	return p.b.(storage.LocalArtifactLister).List(ctx, prefix)
}

func scanSidecarNaming(image string) string {
	return `{"image":"` + image + `","findings":{"CRITICAL":0},"fix_available_findings":{"CRITICAL":0}}`
}

func TestJudgeBaseAdoption(t *testing.T) {
	cases := []struct {
		name       string
		uncached   bool   // single-box storage: no read-through cache
		generation string // configured; default adoptionGenNew
		marker     string // this node's cache marker
		scan       string // legacy shared logical scan sidecar body; "" = none
		digest     bool   // legacy shared logical digest sidecar exists
		genScan    string // scan sidecar under the generation's own keys; "" = none
		genDigest  bool   // digest sidecar under the generation's own keys exists
		otherGen   string // another generation published under its own keys; "" = none
		readErr    error
		want       BaseAdoption
		wantErr    bool
	}{
		// production-us 2026-10-24: fsn-2 rolled to rc.293, its pre-stage
		// published nothing and the shared scan kept the old generation.
		{name: "rc.293: shared copy and cache both hold the previous generation", marker: adoptionGenOld, scan: scanSidecarNaming(adoptionGenOld), digest: true,
			want: BaseAdoption{Cached: adoptionGenOld, Published: adoptionGenOld, Verdict: BaseAdoptionStale}},
		{name: "this node's pre-stage committed the generation", marker: adoptionGenNew, scan: scanSidecarNaming(adoptionGenOld), digest: true,
			want: BaseAdoption{Cached: adoptionGenNew, Verdict: BaseAdoptionCached}},
		{name: "another node published the generation under its own keys", marker: adoptionGenOld, genScan: scanSidecarNaming(adoptionGenNew), genDigest: true,
			want: BaseAdoption{Cached: adoptionGenOld, Published: adoptionGenNew, Verdict: BaseAdoptionPublished}},
		// production-us 2026-10-26: rc.296's pre-stage published, then fsn-3's
		// rc.294 imaged restaged the logical key with its own generation.
		{name: "2026-10-26: an older release restaged the logical key after the generation was published", marker: adoptionGenOld,
			scan: scanSidecarNaming(adoptionGenOld), digest: true, genScan: scanSidecarNaming(adoptionGenNew), genDigest: true,
			want: BaseAdoption{Cached: adoptionGenOld, Published: adoptionGenNew, Verdict: BaseAdoptionPublished}},
		{name: "only a release before generation keys published the generation", marker: adoptionGenOld, scan: scanSidecarNaming(adoptionGenNew), digest: true,
			want: BaseAdoption{Cached: adoptionGenOld, Published: adoptionGenNew, Verdict: BaseAdoptionLegacy}},
		{name: "never staged by any node", want: BaseAdoption{Verdict: BaseAdoptionUnpublished}},
		// Releases with generation keys never write the logical key: a base
		// first published by one changes generation without a legacy copy.
		{name: "only another generation was published, under its own keys", marker: adoptionGenOld, otherGen: adoptionGenOld,
			want: BaseAdoption{Cached: adoptionGenOld, Published: adoptionGenOld, Verdict: BaseAdoptionStale}},
		{name: "digest without its scan commit marker", digest: true, want: BaseAdoption{Verdict: BaseAdoptionStale}},
		{name: "generation digest without its scan commit marker", scan: scanSidecarNaming(adoptionGenNew), digest: true, genDigest: true,
			want: BaseAdoption{Verdict: BaseAdoptionStale}},
		{name: "a rebuild's refusal names no generation", scan: scanSidecarNaming(uncommittedBaseScanImage), digest: true,
			want: BaseAdoption{Verdict: BaseAdoptionStale}},
		// vmmd refuses during the rebuild rather than read older bytes from
		// the legacy key.
		{name: "a rebuild of the generation is in flight under its own keys", scan: scanSidecarNaming(adoptionGenNew), digest: true,
			genScan: scanSidecarNaming(uncommittedBaseScanImage), genDigest: true, want: BaseAdoption{Verdict: BaseAdoptionStale}},
		{name: "torn scan sidecar", scan: "{not json", digest: true, want: BaseAdoption{Verdict: BaseAdoptionStale}},
		// An unnamed dev node may configure a tag; vmmd accepts a scan of
		// that repository's digest (fcvm.scanNamesConfiguredBase).
		{name: "tag configuration and a scan of its digest", generation: "registry.gregale.dev/base-minimal:latest", genScan: scanSidecarNaming(adoptionGenNew), genDigest: true,
			want: BaseAdoption{Published: adoptionGenNew, Verdict: BaseAdoptionPublished}},
		// Single-box storage is no other node's: its logical key is the
		// publication, and nothing routes to generation keys.
		{name: "uncached storage publishing the generation", uncached: true, scan: scanSidecarNaming(adoptionGenNew), digest: true,
			want: BaseAdoption{Published: adoptionGenNew, Verdict: BaseAdoptionPublished}},
		{name: "uncached storage publishing another generation", uncached: true, scan: scanSidecarNaming(adoptionGenOld), digest: true,
			want: BaseAdoption{Published: adoptionGenOld, Verdict: BaseAdoptionStale}},
		{name: "unreadable shared store", marker: adoptionGenOld, readErr: errors.New("registry unavailable"), wantErr: true},
		{name: "no configured generation", generation: "-", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			local, err := storage.NewLocalStorageBackend(filepath.Join(dir, "shared"))
			if err != nil {
				t.Fatal(err)
			}
			parent := sharedParent{b: local}
			var be storage.StorageBackend = parent
			if !tc.uncached {
				cache, err := storage.NewLocalCacheBackend(parent, filepath.Join(dir, "cache"), 1<<30)
				if err != nil {
					t.Fatal(err)
				}
				if tc.marker != "" {
					if err := cache.Put(t.Context(), adoptionKey, strings.NewReader("ext4")); err != nil {
						t.Fatal(err)
					}
					if err := cache.MarkGeneration(adoptionKey, tc.marker); err != nil {
						t.Fatal(err)
					}
				}
				be = cache
			}
			generation := tc.generation
			switch generation {
			case "":
				generation = adoptionGenNew
			case "-":
				generation = ""
			}
			generationKey := storagekey.BaseGeneration(adoptionKey, generation)
			for key, body := range map[string]string{
				wire.ScanKeyForBaseKey(adoptionKey):   tc.scan,
				wire.ScanKeyForBaseKey(generationKey): tc.genScan,
			} {
				if body == "" {
					continue
				}
				if err := local.Put(t.Context(), key, strings.NewReader(body)); err != nil {
					t.Fatal(err)
				}
			}
			if tc.otherGen != "" {
				other := storagekey.BaseGeneration(adoptionKey, tc.otherGen)
				for key, body := range map[string]string{other: "ext4", other + ".digest": "sha256:config", wire.ScanKeyForBaseKey(other): scanSidecarNaming(tc.otherGen)} {
					if err := local.Put(t.Context(), key, strings.NewReader(body)); err != nil {
						t.Fatal(err)
					}
				}
			}
			for key, exists := range map[string]bool{adoptionKey + ".digest": tc.digest, generationKey + ".digest": tc.genDigest} {
				if !exists {
					continue
				}
				if err := local.Put(t.Context(), key, strings.NewReader("sha256:config")); err != nil {
					t.Fatal(err)
				}
			}
			if tc.readErr != nil {
				parent.readErr = tc.readErr
				cache, err := storage.NewLocalCacheBackend(parent, filepath.Join(dir, "cache"), 1<<30)
				if err != nil {
					t.Fatal(err)
				}
				be = cache
			}
			got, err := JudgeBaseAdoption(t.Context(), be, adoptionKey, generation)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			tc.want.Key, tc.want.Generation = adoptionKey, generation
			if got != tc.want {
				t.Fatalf("adoption = %+v, want %+v", got, tc.want)
			}
			if got.Adoptable() == (tc.want.Verdict == BaseAdoptionStale) {
				t.Fatalf("Adoptable() = %v for verdict %q", got.Adoptable(), got.Verdict)
			}
		})
	}
}

// Judging reads the shared copy without installing it in the node cache:
// a serving vmmd here keeps reading the scan it last adopted.
func TestJudgeBaseAdoptionLeavesTheCachedScanAlone(t *testing.T) {
	dir := t.TempDir()
	local, err := storage.NewLocalStorageBackend(filepath.Join(dir, "shared"))
	if err != nil {
		t.Fatal(err)
	}
	cache, err := storage.NewLocalCacheBackend(sharedParent{b: local}, filepath.Join(dir, "cache"), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	scanKey := wire.ScanKeyForBaseKey(adoptionKey)
	if err := cache.Put(t.Context(), scanKey, strings.NewReader(scanSidecarNaming(adoptionGenOld))); err != nil {
		t.Fatal(err)
	}
	generationScan := wire.ScanKeyForBaseKey(storagekey.BaseGeneration(adoptionKey, adoptionGenNew))
	if err := local.Put(t.Context(), generationScan, strings.NewReader(scanSidecarNaming(adoptionGenNew))); err != nil {
		t.Fatal(err)
	}
	if got, err := JudgeBaseAdoption(t.Context(), cache, adoptionKey, adoptionGenNew); err != nil || got.Verdict != BaseAdoptionPublished {
		t.Fatalf("adoption = %+v, %v; want published", got, err)
	}
	rc, err := cache.Get(t.Context(), scanKey)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(body) != scanSidecarNaming(adoptionGenOld) {
		t.Fatalf("cached scan sidecar replaced by the judge: %s", body)
	}
}

// vmmd's configuredRuntimeBaseGenerations iterates this list, so the
// pre-stage publishes exactly the bases the boot gate checks.
func TestGatedRuntimeBases(t *testing.T) {
	want := []string{"", RuntimeNode22, RuntimePython312, RuntimeGo124, RuntimeGo124Alpine, RuntimeNode24, RuntimePython313}
	if got := GatedRuntimeBases(); !reflect.DeepEqual(got, want) {
		t.Fatalf("GatedRuntimeBases() = %q, want %q", got, want)
	}
}
