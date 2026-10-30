package imaged

// adr: 20261026181418 — the node cache keeps one entry per logical base key
// for every release on the node; scan evidence vouches for the generation's
// shared publication.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/storagekey"
	"github.com/onebox-faas/faas/pkg/wire"
)

const (
	sharedCacheBaseKey   = "base/base-amd64.ext4"
	sharedCacheDigestKey = sharedCacheBaseKey + ".digest"
	sharedCacheOtherRef  = "registry.example/base@sha256:8da41bd00000000000000000000000000000000000000000000000000000000a"
)

// scanWrite is one write of a shared scan sidecar. scanned is false for the
// refusal written before a scan runs.
type scanWrite struct {
	image    string
	critical int
	scanned  bool
}

// scanWriteRecorder stands in for shared storage (no local paths) and records
// every scan sidecar written to it.
type scanWriteRecorder struct {
	storage.StorageBackend
	mu     sync.Mutex
	writes map[string][]scanWrite
}

func (r *scanWriteRecorder) Put(ctx context.Context, key string, body io.Reader) error {
	if !strings.HasPrefix(key, "scans/") {
		return r.StorageBackend.Put(ctx, key, body)
	}
	raw, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	var value baseScanCompatibility
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	r.mu.Lock()
	r.writes[key] = append(r.writes[key], scanWrite{image: value.Image, critical: value.Findings[SeverityCritical], scanned: !value.ScannedAt.IsZero()})
	r.mu.Unlock()
	return r.StorageBackend.Put(ctx, key, bytes.NewReader(raw))
}

func (r *scanWriteRecorder) written(key string) []scanWrite {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]scanWrite(nil), r.writes[key]...)
}

// requireNoFailedScanOf fails when the sidecar of a failed scan of ref was
// written, or the last write does not vouch for ref. The refusals written
// before a first scan runs (unscanned) are replaced by its result.
func requireNoFailedScanOf(t *testing.T, writes []scanWrite, ref string) {
	t.Helper()
	if len(writes) == 0 {
		t.Fatal("no scan sidecar written")
	}
	for _, w := range writes {
		if w.critical >= wire.BaseScanFailedCount && w.scanned {
			t.Fatalf("published the fail-closed sidecar of a failed scan: %+v", writes)
		}
	}
	if last := writes[len(writes)-1]; last.image != ref || last.critical != 0 || !last.scanned {
		t.Fatalf("final scan sidecar %+v does not vouch for %s", last, ref)
	}
}

// nodeCache opens this node's cache as one daemon does: the same directory
// every daemon on the node shares, with the base routed to the generation
// that daemon's release configures.
func nodeCache(t *testing.T, parent storage.StorageBackend, root, generation string) *storage.LocalCacheBackend {
	t.Helper()
	cache, err := storage.NewLocalCacheBackend(parent, root, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	routed, err := RouteBaseGenerations(cache, map[string]string{sharedCacheBaseKey: generation})
	if err != nil || len(routed) != 1 {
		t.Fatalf("route %s: %v %v", generation, routed, err)
	}
	return cache
}

// production-us rc.305, 2026-10-29 21:28 UTC, fsn-2: the release's pre-stage
// built base-minimal's new generation into the node cache, and while it
// scanned, the serving rc.304 imaged converged the same cache entry back to
// rc.304's publication (ADR-567). The scan's stored-artifact check read
// rc.304's bytes, failed with artifact_mismatch, and published CRITICAL=9999
// under the new generation's keys; fsn-2 refused every minimal-base boot for
// about two hours. Here the serving daemon's real convergence runs inside the
// pre-stage's scan.
func TestPrestageScanSurvivesServingConvergenceOfTheSharedCacheEntry(t *testing.T) {
	h, store, _, _, ref := verifiedBaseFixture(t, "complete")
	local, err := h.storageFor()
	if err != nil {
		t.Fatal(err)
	}
	shared := &scanWriteRecorder{StorageBackend: local, writes: map[string][]scanWrite{}}
	cacheRoot := filepath.Join(t.TempDir(), "cache")

	// The serving N-1 imaged: its generation is published under its own keys
	// and is what this node's cache holds.
	serving := nodeCache(t, shared, cacheRoot, sharedCacheOtherRef)
	servingGuest, err := guestInitBinaryDigest(h.guestInitPath)
	if err != nil {
		t.Fatal(err)
	}
	servingBytes := bytes.Repeat([]byte{1}, 2048)
	if err := serving.Put(t.Context(), sharedCacheBaseKey, bytes.NewReader(servingBytes)); err != nil {
		t.Fatal(err)
	}
	digest := baseDigestSidecarValueWithSource(imagechain.Digest([]byte("serving config")), servingGuest, sharedCacheOtherRef)
	if err := serving.Put(t.Context(), sharedCacheDigestKey, strings.NewReader(digest)); err != nil {
		t.Fatal(err)
	}
	content, err := hashContent(bytes.NewReader(servingBytes))
	if err != nil {
		t.Fatal(err)
	}
	if err := putBaseContent(t.Context(), serving, sharedCacheBaseKey, content); err != nil {
		t.Fatal(err)
	}
	servingDaemon := &Handler{log: silentLogger()}

	prestage := nodeCache(t, shared, cacheRoot, ref)
	converged := false
	h.WithStorage(prestage).WithGrypeRun(func(ctx context.Context, _ string) (*ScanResult, error) {
		if !converged {
			replaced, err := servingDaemon.convergeBase(ctx, serving, sharedCacheBaseKey,
				stagedBase{ref: sharedCacheOtherRef, digestKey: sharedCacheDigestKey}, servingGuest)
			if err != nil || !replaced {
				t.Fatalf("serving convergence did not replace the cached base: %t %v", replaced, err)
			}
			converged = true
		}
		return producedScanResult(t, false), nil
	})

	if _, err := h.EnsureBaseExt4(t.Context(), ref, sharedCacheBaseKey, sharedCacheDigestKey, "", "", ""); err != nil {
		t.Fatalf("pre-stage: %v", err)
	}
	if !converged || !bytes.Equal(readStored(t, serving, sharedCacheBaseKey), servingBytes) {
		t.Fatal("fixture: the serving daemon must have put its generation back in the cache during the scan")
	}
	scan, err := store.GetCurrentBaseImageScan(t.Context(), sharedCacheBaseKey)
	if err != nil || scan.Input.Status != "complete" {
		t.Fatalf("pre-stage scan of the new generation = %q (%s), %v; want complete", scan.Input.Status, scan.Input.Failure, err)
	}
	genScan := wire.ScanKeyForBaseKey(storagekey.BaseGeneration(sharedCacheBaseKey, ref))
	requireNoFailedScanOf(t, shared.written(genScan), ref)
}

// The other direction: the serving daemon's deploy-time scan of its own
// generation runs while a candidate release's pre-stage has put the next
// generation in the shared cache entry. It must keep vouching for its
// generation's publication rather than fail closed for every node on it.
func TestServingScanSurvivesPrestageReplacingTheSharedCacheEntry(t *testing.T) {
	h, store, _, _, ref := verifiedBaseFixture(t, "complete")
	local, err := h.storageFor()
	if err != nil {
		t.Fatal(err)
	}
	shared := &scanWriteRecorder{StorageBackend: local, writes: map[string][]scanWrite{}}
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	serving := nodeCache(t, shared, cacheRoot, ref)
	h.WithStorage(serving).WithGrypeRun(func(context.Context, string) (*ScanResult, error) { return producedScanResult(t, false), nil })
	base, err := h.EnsureBaseExt4(t.Context(), ref, sharedCacheBaseKey, sharedCacheDigestKey, "", "", "")
	if err != nil {
		t.Fatal(err)
	}

	genScan := wire.ScanKeyForBaseKey(storagekey.BaseGeneration(sharedCacheBaseKey, ref))
	staged := len(shared.written(genScan))
	candidate := nodeCache(t, shared, cacheRoot, sharedCacheOtherRef)
	if err := candidate.Put(t.Context(), sharedCacheBaseKey, bytes.NewReader(bytes.Repeat([]byte{2}, 2048))); err != nil {
		t.Fatal(err)
	}
	h.WithGrypeRun(func(context.Context, string) (*ScanResult, error) {
		t.Fatal("a fresh complete scan of the unchanged publication was rerun")
		return nil, nil
	})
	value, err := h.ensureProducedBaseScan(t.Context(), serving, base.Producer, "")
	if err != nil || value.Input.Status != "complete" {
		t.Fatalf("serving scan = %q, %v; want the complete scan reused", value.Input.Status, err)
	}
	if current, err := store.GetCurrentBaseImageScan(t.Context(), sharedCacheBaseKey); err != nil || current.ID != value.ID {
		t.Fatalf("current scan changed: %v", err)
	}
	// Not even the refusal a rescan writes first (Hunt 14 H14-117): vmmd on
	// every node of this generation would refuse boots until it was replaced.
	writes := shared.written(genScan)
	requireNoFailedScanOf(t, writes, ref)
	for _, w := range writes[staged:] {
		if w.critical != 0 {
			t.Fatalf("reuse wrote a refusal: %+v", writes[staged:])
		}
	}
}
