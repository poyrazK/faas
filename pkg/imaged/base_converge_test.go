// adr: 568
package imaged

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/storage"
)

const (
	convergeBaseKey   = "base/runner-node22-amd64.ext4"
	convergeDigestKey = convergeBaseKey + ".digest"
	convergeRef       = "ghcr.io/onebox-faas/runner-node22@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	convergeConfig    = "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	otherRef          = "ghcr.io/onebox-faas/runner-node22@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	otherGuestInit    = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

type convergeFixture struct {
	h      *Handler
	parent *fakeOCIPutter
	cache  *storage.LocalCacheBackend
	staged stagedBase
	guest  string
}

// newConvergeFixture models production-us after a release: this node built
// and cached its own copy of the base ("own-build"), then another node
// republished the same recipe with different bytes ("other-node-build").
func newConvergeFixture(t *testing.T) convergeFixture {
	t.Helper()
	guestPath := filepath.Join(t.TempDir(), "init")
	if err := os.WriteFile(guestPath, []byte("guest-init-v2"), 0o755); err != nil {
		t.Fatal(err)
	}
	guest, err := guestInitBinaryDigest(guestPath)
	if err != nil {
		t.Fatal(err)
	}
	parent := newFakeOCIPutter()
	cache, err := storage.NewLocalCacheBackend(parent, t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := cache.Put(ctx, convergeBaseKey, bytes.NewReader([]byte("own-build"))); err != nil {
		t.Fatal(err)
	}
	parent.blobs[convergeBaseKey] = []byte("other-node-build")
	parent.blobs[convergeDigestKey] = []byte(baseDigestSidecarValueWithSource(convergeConfig, guest, convergeRef))
	h := &Handler{log: silentLogger(), storage: cache, guestInitPath: guestPath}
	return convergeFixture{h: h, parent: parent, cache: cache, staged: stagedBase{ref: convergeRef, digestKey: convergeDigestKey}, guest: guest}
}

func (f convergeFixture) publishContent(t *testing.T, body string) {
	t.Helper()
	content, err := hashContent(bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	f.parent.blobs[baseContentKey(convergeBaseKey)] = raw
}

func (f convergeFixture) local(t *testing.T) string {
	t.Helper()
	path, ok, err := f.cache.LocalPath(convergeBaseKey)
	if err != nil || !ok {
		t.Fatalf("base not cached locally: ok=%v err=%v", ok, err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (f convergeFixture) converge(t *testing.T) bool {
	t.Helper()
	changed, err := f.h.convergeBase(context.Background(), f.cache, convergeBaseKey, f.staged, f.guest)
	if err != nil {
		t.Fatalf("convergeBase: %v", err)
	}
	return changed
}

func TestConvergeBaseAdoptsTheSharedPublication(t *testing.T) {
	f := newConvergeFixture(t)
	f.publishContent(t, "other-node-build")
	if !f.converge(t) {
		t.Fatal("divergent local base was not replaced")
	}
	if got := f.local(t); got != "other-node-build" {
		t.Fatalf("local base = %q, want the published bytes", got)
	}
	if f.converge(t) {
		t.Fatal("an aligned base was refreshed again")
	}
}

func TestConvergeBaseKeepsLocalCopyForAnotherRecipe(t *testing.T) {
	cases := map[string]func(guest string) string{
		"other source ref": func(guest string) string { return baseDigestSidecarValueWithSource(convergeConfig, guest, otherRef) },
		"other guest-init": func(string) string {
			return baseDigestSidecarValueWithSource(convergeConfig, otherGuestInit, convergeRef)
		},
	}
	for name, sidecar := range cases {
		t.Run(name, func(t *testing.T) {
			f := newConvergeFixture(t)
			f.parent.blobs[convergeDigestKey] = []byte(sidecar(f.guest))
			f.publishContent(t, "other-node-build")
			if f.converge(t) {
				t.Fatal("adopted a publication built from a different recipe")
			}
			if got := f.local(t); got != "own-build" {
				t.Fatalf("local base = %q, want it untouched", got)
			}
		})
	}
}

func TestConvergeBaseBackfillsAMissingContentSidecar(t *testing.T) {
	f := newConvergeFixture(t)
	if !f.converge(t) {
		t.Fatal("a pre-ADR-568 publication was not adopted")
	}
	if got := f.local(t); got != "other-node-build" {
		t.Fatalf("local base = %q, want the published bytes", got)
	}
	var recorded baseContent
	if err := json.Unmarshal(f.parent.blobs[baseContentKey(convergeBaseKey)], &recorded); err != nil {
		t.Fatalf("content sidecar: %v", err)
	}
	want, _ := hashContent(bytes.NewReader([]byte("other-node-build")))
	if recorded != want {
		t.Fatalf("content sidecar = %+v, want %+v", recorded, want)
	}
}

func TestConvergeBaseDoesNotRedownloadATornPublication(t *testing.T) {
	f := newConvergeFixture(t)
	// The sidecar names bytes the parent does not (yet) hold.
	f.publishContent(t, "publisher-still-uploading")
	if !f.converge(t) {
		t.Fatal("first mismatch did not attempt a refresh")
	}
	gets := f.parent.gets
	if f.converge(t) {
		t.Fatal("refreshed again for the same unresolved sidecar")
	}
	// Two sidecar reads, no base download.
	if f.parent.gets != gets+2 {
		t.Fatalf("parent gets = %d, want %d (sidecars only)", f.parent.gets, gets+2)
	}
	f.publishContent(t, "other-node-build")
	if f.converge(t) {
		t.Fatal("refreshed although the local copy already matches the corrected sidecar")
	}
}

func TestWriteBaseContentSidecarRecordsThePublishedBytes(t *testing.T) {
	f := newConvergeFixture(t)
	if err := f.h.writeBaseContentSidecar(context.Background(), f.cache, convergeBaseKey); err != nil {
		t.Fatal(err)
	}
	var recorded baseContent
	if err := json.Unmarshal(f.parent.blobs[baseContentKey(convergeBaseKey)], &recorded); err != nil {
		t.Fatalf("content sidecar: %v", err)
	}
	want, _ := hashContent(bytes.NewReader([]byte("own-build")))
	if recorded != want || !recorded.valid() {
		t.Fatalf("content sidecar = %+v, want %+v", recorded, want)
	}
}

func TestEnsureBaseExt4RemembersStagedBases(t *testing.T) {
	h := &Handler{log: silentLogger()}
	h.rememberStagedBase(convergeRef, convergeBaseKey, convergeDigestKey)
	if got := h.baseConvergence.staged[convergeBaseKey]; got != (stagedBase{ref: convergeRef, digestKey: convergeDigestKey}) {
		t.Fatalf("staged = %+v", got)
	}
}
