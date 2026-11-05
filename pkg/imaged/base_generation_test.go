package imaged

// adr: 20261026181418 — each release stages and reads its runtime base
// generation under that generation's own shared keys, and never moves the
// shared logical key another release reads.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/storagekey"
	"github.com/onebox-faas/faas/pkg/wire"
)

// mixedFleet is shared storage and one node cache per daemon set.
type mixedFleet struct {
	t      *testing.T
	dir    string
	shared storage.StorageBackend
}

func newMixedFleet(t *testing.T) mixedFleet {
	t.Helper()
	dir := t.TempDir()
	local, err := storage.NewLocalStorageBackend(filepath.Join(dir, "shared"))
	if err != nil {
		t.Fatal(err)
	}
	return mixedFleet{t: t, dir: dir, shared: local}
}

// node returns a fresh node cache over the shared store.
func (f mixedFleet) node(name string) *storage.LocalCacheBackend {
	f.t.Helper()
	cache, err := storage.NewLocalCacheBackend(sharedParent{b: f.shared}, filepath.Join(f.dir, name), 1<<30)
	if err != nil {
		f.t.Fatal(err)
	}
	return cache
}

// scanImage is the generation a shared scan sidecar names, read by its exact
// key; "" when there is none.
func (f mixedFleet) scanImage(baseKey string) string {
	f.t.Helper()
	rc, err := f.shared.Get(f.t.Context(), wire.ScanKeyForBaseKey(baseKey))
	if err != nil {
		return ""
	}
	defer func() { _ = rc.Close() }()
	var scan struct{ Image string }
	_ = json.NewDecoder(rc).Decode(&scan)
	return scan.Image
}

// production-us 2026-10-26: fsn-2's rc.296 pre-stage published base-minimal's
// new generation, then a source build on fsn-3 made rc.294's imaged restage
// base-minimal, and fsn-2's vmmd refused every cold boot. Each case runs the
// real staging paths in that order and judges both generations as vmmd will.
func TestStagingKeepsEachReleasesGeneration(t *testing.T) {
	cases := []struct {
		name string
		// n1Routed: release N-1 has generation keys too. False is a release
		// before them, which writes the shared logical key: a vmmd of that
		// release reads it, so it must still name N-1.
		n1Routed bool
		wantN1   string // N-1's verdict on a node that never cached it
	}{
		{name: "2026-10-26: an N-1 imaged before generation keys restages after N's pre-stage", wantN1: BaseAdoptionLegacy},
		{name: "N-1 has generation keys and restages after N's pre-stage", n1Routed: true, wantN1: BaseAdoptionPublished},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const arch = "amd64"
			ctx := t.Context()
			baseKey := storagekey.BaseForArch("", arch)
			fleet := newMixedFleet(t)
			hN, _, _, _, refN := verifiedBaseFixtureOf(t, "complete", "release N")
			hN1, storeN1, _, _, refN1 := verifiedBaseFixtureOf(t, "complete", "release N-1")
			for _, h := range []*Handler{hN, hN1} {
				h.WithGrypeRun(func(context.Context, string) (*ScanResult, error) { return producedScanResult(t, false), nil })
			}
			envN := func(key string) string { return map[string]string{"FAAS_DEPLOY_BASE_REF_MINIMAL": refN}[key] }
			envN1 := func(key string) string { return map[string]string{"FAAS_DEPLOY_BASE_REF_MINIMAL": refN1}[key] }
			n1Node := func(name string) *Handler {
				hN1.WithStorage(fleet.node(name))
				if tc.n1Routed {
					if _, err := hN1.RouteBaseGenerations(arch, envN1); err != nil {
						t.Fatal(err)
					}
				}
				return hN1
			}

			// fsn-3 serves N-1 and has staged its base.
			if _, err := n1Node("fsn-3").EnsureMinimalBase(ctx, arch, envN1); err != nil {
				t.Fatal(err)
			}
			// fsn-2 rolls to N; its pre-stage publishes N's generation.
			fsn2 := fleet.node("fsn-2")
			hN.WithStorage(fsn2)
			if routed, err := hN.RouteBaseGenerations(arch, envN); err != nil || len(routed) != len(GatedRuntimeBases()) {
				t.Fatalf("routed %q, %v; want every gated base", routed, err)
			}
			release, err := hN.EnsureReleaseBases(ctx, arch, envN, AssignedBasesResult{})
			if err != nil || !reflect.DeepEqual(release.Staged, []string{""}) {
				t.Fatalf("N pre-stage = %+v, %v; want the minimal base staged", release, err)
			}
			// 17:03: a source build on another N-1 node restages N-1, and
			// republishes it whole.
			if _, err := n1Node("fsn-3-rebuilt").EnsureMinimalBase(ctx, arch, envN1); err != nil {
				t.Fatal(err)
			}
			if producer, err := storeN1.GetCurrentBaseImageProducer(ctx, baseKey); err != nil || producer.Input.SourceReference != refN1 {
				t.Fatalf("N-1 producer = %+v, %v", producer.Input.SourceReference, err)
			}

			// N's pre-stage never moved the logical key a release before
			// generation keys reads, and N-1 never moved N's keys.
			wantLogical := refN1
			if tc.n1Routed {
				wantLogical = "" // no release wrote it
			}
			if got := fleet.scanImage(baseKey); got != wantLogical {
				t.Fatalf("shared logical scan names %q, want %q", got, wantLogical)
			}
			if got := fleet.scanImage(storagekey.BaseGeneration(baseKey, refN)); got != refN {
				t.Fatalf("N's generation scan names %q, want %q", got, refN)
			}
			// Every node that has not cached a generation adopts its own.
			for i, judge := range []struct {
				generation, want string
			}{{refN, BaseAdoptionPublished}, {refN1, tc.wantN1}} {
				got, err := JudgeBaseAdoption(ctx, fleet.node(fmt.Sprintf("fresh-%d", i)), baseKey, judge.generation)
				if err != nil || got.Verdict != judge.want || got.Published != judge.generation {
					t.Fatalf("generation %s on a fresh node: %+v, %v; want %s", judge.generation, got, err, judge.want)
				}
			}
			// fsn-2 commits N, from bytes it published under N's keys.
			if marked, ok, err := fsn2.CachedGeneration(baseKey); err != nil || !ok || marked != refN {
				t.Fatalf("fsn-2 marker = %q, %v, %v; want N", marked, ok, err)
			}
			rc, err := fleet.shared.Get(ctx, storagekey.BaseGeneration(baseKey, refN))
			if err != nil {
				t.Fatalf("N's generation base was not published: %v", err)
			}
			_ = rc.Close()
		})
	}
}

// putRecorder records every key Put to shared storage, in order.
type putRecorder struct {
	storage.StorageBackend
	puts []string
}

func (r *putRecorder) Put(ctx context.Context, key string, body io.Reader) error {
	r.puts = append(r.puts, key)
	return r.StorageBackend.Put(ctx, key, body)
}

// A release with generation keys upgrading from one without: a reused base
// verified from the node cache is published under its generation before its
// digest and scan, so no node adopts a scan whose base is missing.
func TestReusedBaseIsPublishedUnderItsGenerationFirst(t *testing.T) {
	const arch = "amd64"
	ctx := t.Context()
	baseKey := storagekey.BaseForArch("", arch)
	fleet := newMixedFleet(t)
	shared := &putRecorder{StorageBackend: sharedParent{b: fleet.shared}}
	node, err := storage.NewLocalCacheBackend(shared, filepath.Join(fleet.dir, "fsn-2"), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	h, _, _, runner, ref := verifiedBaseFixtureOf(t, "complete", "unchanged generation")
	h.WithGrypeRun(func(context.Context, string) (*ScanResult, error) { return producedScanResult(t, false), nil })
	env := func(key string) string { return map[string]string{"FAAS_DEPLOY_BASE_REF_MINIMAL": ref}[key] }
	// The previous release staged it under the logical key only.
	h.WithStorage(node)
	if _, err := h.EnsureMinimalBase(ctx, arch, env); err != nil {
		t.Fatal(err)
	}
	generation := baseGroupKeys(storagekey.BaseGeneration(baseKey, ref))
	if _, err := fleet.shared.Get(ctx, generation[0]); err == nil {
		t.Fatal("fixture: an unrouted stage published the generation key")
	}
	// The next release routes the same node cache and reuses the producer.
	if _, err := h.RouteBaseGenerations(arch, env); err != nil {
		t.Fatal(err)
	}
	shared.puts, runner.argv = nil, nil
	res, err := h.EnsureMinimalBase(ctx, arch, env)
	if err != nil || !res.Skipped || len(runner.argv) != 0 {
		t.Fatalf("EnsureMinimalBase = %+v, %v, mkfs %v; want a reuse", res, err, runner.argv)
	}
	order := make(map[string]int, len(shared.puts))
	for i, key := range shared.puts {
		if _, seen := order[key]; !seen {
			order[key] = i
		}
	}
	for _, key := range generation {
		if _, ok := order[key]; !ok {
			t.Fatalf("the reuse did not publish %s: %q", key, shared.puts)
		}
	}
	if order[generation[0]] > order[generation[1]] || order[generation[0]] > order[generation[3]] {
		t.Fatalf("published %q: the digest or scan before the base", shared.puts)
	}
	for _, key := range shared.puts {
		if !strings.HasPrefix(key, storagekey.BaseGenerationsPrefix) && !strings.HasPrefix(key, "scans/generations/") {
			t.Fatalf("the routed reuse wrote the shared logical key %s", key)
		}
	}
	if got, err := JudgeBaseAdoption(ctx, fleet.node("fsn-3"), baseKey, ref); err != nil || got.Verdict != BaseAdoptionPublished {
		t.Fatalf("fsn-3 adoption = %+v, %v; want published", got, err)
	}
}

func TestRouteBaseGenerations(t *testing.T) {
	digest := strings.Repeat("a", 64)
	pinned := map[string]string{
		"FAAS_NODE_NAME":               "fsn-test.faas",
		"FAAS_DEPLOY_BASE_REF_MINIMAL": "ghcr.io/example/minimal@sha256:" + digest,
	}
	for _, row := range DefaultRuntimeBaseRefs {
		pinned[row.EnvOverride] = "ghcr.io/example/" + row.Runtime + "@sha256:" + digest
	}
	cases := []struct {
		name       string
		storage    func(t *testing.T) storage.StorageBackend
		env        map[string]string
		wantRouted int
	}{
		{name: "shared storage routes every gated base", env: pinned, wantRouted: len(GatedRuntimeBases()),
			storage: func(t *testing.T) storage.StorageBackend { return newMixedFleet(t).node("n") }},
		{name: "single-box storage routes nothing", env: pinned,
			storage: func(t *testing.T) storage.StorageBackend { return mustLocalStorage(t, t.TempDir()) }},
		{name: "a base whose ref does not resolve stays unrouted", env: map[string]string{"FAAS_NODE_NAME": "fsn-test.faas", "FAAS_DEPLOY_BASE_REF_MINIMAL": pinned["FAAS_DEPLOY_BASE_REF_MINIMAL"]},
			wantRouted: 1, storage: func(t *testing.T) storage.StorageBackend { return newMixedFleet(t).node("n") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			be := tc.storage(t)
			h := New(nil, &fakeNotifier{}, nil, nil, "", t.TempDir(), silentLogger()).WithStorage(be)
			routed, err := h.RouteBaseGenerations("amd64", func(key string) string { return tc.env[key] })
			if err != nil || len(routed) != tc.wantRouted {
				t.Fatalf("routed %q, %v; want %d bases", routed, err, tc.wantRouted)
			}
			for _, baseKey := range routed {
				cache, _, err := storage.CacheBackendForKey(be, baseKey)
				if err != nil || cache == nil {
					t.Fatal(err)
				}
				generation := storagekey.BaseGeneration(baseKey, tc.env["FAAS_DEPLOY_BASE_REF_MINIMAL"])
				if baseKey != storagekey.BaseForArch("", "amd64") {
					continue
				}
				published := baseGroupKeys(generation)
				for i, key := range baseGroupKeys(baseKey) {
					if got, ok := cache.GenerationRoute(key); !ok || got != published[i] {
						t.Fatalf("%s routes to %q, %v; want %q", key, got, ok, published[i])
					}
				}
			}
		})
	}
}

// Staging a ref under a base key routed to another generation would publish
// its bytes as that generation.
func TestStagingRefusesAnotherGenerationUnderARoutedKey(t *testing.T) {
	h, _, _, _, ref := verifiedBaseFixtureOf(t, "complete", "routed elsewhere")
	node := newMixedFleet(t).node("n")
	h.WithStorage(node)
	other := "ghcr.io/example/minimal@sha256:" + strings.Repeat("b", 64)
	if _, err := h.RouteBaseGenerations("amd64", func(key string) string {
		return map[string]string{"FAAS_DEPLOY_BASE_REF_MINIMAL": other}[key]
	}); err != nil {
		t.Fatal(err)
	}
	baseKey := storagekey.BaseForArch("", "amd64")
	_, err := h.EnsureBaseExt4(t.Context(), ref, baseKey, baseKey+".digest", "", "", "")
	if err == nil || !strings.Contains(err.Error(), "is routed to") {
		t.Fatalf("EnsureBaseExt4 = %v, want a routing refusal", err)
	}
	if _, err := node.Get(t.Context(), baseKey); err == nil {
		t.Fatal("the refused stage published a base")
	}
}
