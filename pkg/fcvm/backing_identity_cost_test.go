// adr: 510 — amendment 2: the backing check stays off the wake path's
// critical time without weakening what it verifies.
package fcvm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storage"
)

// memoryBackingRecorder stands in for the trusted xattr: it keeps a digest
// per (device, inode) and outlives a Manager, as the xattr outlives vmmd.
type memoryBackingRecorder struct {
	mu      sync.Mutex
	values  map[string]string
	records int
}

func newMemoryBackingRecorder() *memoryBackingRecorder {
	return &memoryBackingRecorder{values: map[string]string{}}
}

func recorderFileKey(f *os.File) string {
	info, err := f.Stat()
	if err != nil {
		return ""
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return fmt.Sprintf("%d:%d", st.Dev, st.Ino)
}

func (r *memoryBackingRecorder) recorded(f *os.File, size int64) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return parseRecordedBackingDigest(r.values[recorderFileKey(f)], size)
}

func (r *memoryBackingRecorder) record(f *os.File, size int64, digest string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values[recorderFileKey(f)] = formatRecordedBackingDigest(size, digest)
	r.records++
	return nil
}

// countHashes counts full reads of a file version on m.
func countHashes(m *Manager, delay time.Duration) *atomic.Int32 {
	var n atomic.Int32
	m.beforeBackingHash = func(string) {
		n.Add(1)
		time.Sleep(delay)
	}
	return &n
}

// slowStore delays every read, as a sidecar that is not in the node cache
// pays a shared-storage round trip.
type slowStore struct {
	storage.StorageBackend
	delay time.Duration
}

func (s slowStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	time.Sleep(s.delay)
	return s.StorageBackend.Get(ctx, key)
}

// The check reports where its time went, and a node that already knows the
// current files' digests (this process or an earlier one) never reads them
// in full on the wake path.
func TestCheckSnapshotBackingCostAndTimings(t *testing.T) {
	const memKey = "snap/d1/captures/c1/v2/mem"
	const delay = 25 * time.Millisecond
	for _, tc := range []struct {
		name string
		// setup runs after the capture, on the Manager that verifies. It
		// returns that Manager (a fresh one models a vmmd restart).
		setup       func(t *testing.T, f backingFixture, rec *memoryBackingRecorder) *Manager
		slowSidecar bool
		dropSidecar bool
		wantErr     error
		wantHashes  int32
		wantSidecar bool // SidecarMs >= delay
		wantHash    bool // HashMs >= delay
	}{
		{
			name:  "digests known in this process: nothing is read",
			setup: func(_ *testing.T, f backingFixture, _ *memoryBackingRecorder) *Manager { return f.m },
		},
		{
			name: "restart with recorded digests: nothing is read",
			setup: func(_ *testing.T, f backingFixture, rec *memoryBackingRecorder) *Manager {
				return restartedManager(f, rec)
			},
		},
		{
			name: "restart with recorded digests, base replaced: only the base is read and refused",
			setup: func(t *testing.T, f backingFixture, rec *memoryBackingRecorder) *Manager {
				replaceFile(t, f.base, "base-layout-b")
				return restartedManager(f, rec)
			},
			wantErr: ErrSnapshotBackingChanged, wantHashes: 1, wantHash: true,
		},
		{
			name: "restart without recorded digests: kernel and base are read",
			setup: func(_ *testing.T, f backingFixture, _ *memoryBackingRecorder) *Manager {
				return restartedManager(f, newMemoryBackingRecorder())
			},
			wantHashes: 2, wantHash: true,
		},
		{
			name:        "remote sidecar read is attributed to the sidecar",
			setup:       func(_ *testing.T, f backingFixture, _ *memoryBackingRecorder) *Manager { return f.m },
			slowSidecar: true, wantSidecar: true,
		},
		{
			name:        "capture without identity is refused before touching the images",
			setup:       func(_ *testing.T, f backingFixture, _ *memoryBackingRecorder) *Manager { return f.m },
			dropSidecar: true, wantErr: ErrSnapshotBackingUnverified,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			f := newBackingFixture(ctx, t)
			rec := newMemoryBackingRecorder()
			f.m.backingRecorder = rec
			snap := f.capture(ctx, t, memKey)
			if !f.m.awaitBackingForTest(ctx, "i") {
				t.Fatal("capture identity was not recorded")
			}
			m := tc.setup(t, f, rec)
			hashes := countHashes(m, delay)
			if tc.slowSidecar {
				snap.Storage = slowStore{StorageBackend: f.m.storage, delay: delay}
			}
			if tc.dropSidecar {
				snap = &Snapshot{FCVersion: testFCVersion, StorageKey: "snap/d1/captures/none/v2/mem"}
			}
			timings, err := m.checkSnapshotBacking(ctx, snap, f.base)
			if tc.wantErr == nil && err != nil {
				t.Fatalf("check = %v, want nil", err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("check = %v, want %v", err, tc.wantErr)
			}
			if got := hashes.Load(); got != tc.wantHashes {
				t.Fatalf("full reads = %d, want %d", got, tc.wantHashes)
			}
			if timings.TotalMs < timings.SidecarMs+timings.FetchMs+timings.HashMs {
				t.Fatalf("timings %+v: parts exceed the total", timings)
			}
			if got := timings.SidecarMs >= delay.Milliseconds(); got != tc.wantSidecar {
				t.Fatalf("SidecarMs = %d, want >= %d: %v", timings.SidecarMs, delay.Milliseconds(), tc.wantSidecar)
			}
			if got := timings.HashMs >= delay.Milliseconds(); got != tc.wantHash {
				t.Fatalf("HashMs = %d, want >= %d: %v", timings.HashMs, delay.Milliseconds(), tc.wantHash)
			}
			if tc.dropSidecar && (timings.FetchMs != 0 || timings.HashMs != 0) {
				t.Fatalf("refused capture resolved the images: %+v", timings)
			}
		})
	}
}

// restartedManager is a new vmmd process on the same node: an empty memo,
// the same files and the same recorded digests.
func restartedManager(f backingFixture, rec *memoryBackingRecorder) *Manager {
	m := NewManager(&fakeRunner{}, f.vmm, Paths{Kernel: f.kernel}, testFCVersion, nil, nil)
	m.WithStorage(f.m.storage)
	m.backingRecorder = rec
	return m
}

func (m *Manager) awaitBackingForTest(ctx context.Context, instance string) bool {
	_, ok := m.awaitInstanceBacking(ctx, instance)
	return ok
}

func TestRecordedBackingDigestFormat(t *testing.T) {
	const digest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, tc := range []struct {
		name   string
		value  string
		size   int64
		wantOK bool
	}{
		{name: "round trip", value: formatRecordedBackingDigest(4096, digest), size: 4096, wantOK: true},
		{name: "file grew since", value: formatRecordedBackingDigest(4096, digest), size: 8192},
		{name: "unknown version", value: "v2 4096 " + digest, size: 4096},
		{name: "not sha256", value: "v1 4096 md5:0123456789abcdef0123456789abcdef", size: 4096},
		{name: "short digest", value: "v1 4096 sha256:0123", size: 4096},
		{name: "uppercase hex", value: "v1 4096 sha256:0123456789ABCDEF0123456789abcdef0123456789abcdef0123456789abcdef", size: 4096},
		{name: "not hex", value: "v1 4096 sha256:zz23456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", size: 4096},
		{name: "extra field", value: formatRecordedBackingDigest(4096, digest) + " x", size: 4096},
		{name: "empty", value: "", size: 4096},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseRecordedBackingDigest(tc.value, tc.size)
			if ok != tc.wantOK || (ok && got != digest) {
				t.Fatalf("parse(%q, %d) = %q %v, want ok=%v", tc.value, tc.size, got, ok, tc.wantOK)
			}
		})
	}
}

// A base replaced under a running vmmd (a release pre-stage, another
// writer's refresh) is identified by the primer, so the next restore that
// needs it neither reads it in full nor restores onto it.
func TestBackingDigestPrimerFollowsAttachedBases(t *testing.T) {
	ctx := t.Context()
	f := newBackingFixture(ctx, t)
	f.m.backingRecorder = newMemoryBackingRecorder()
	snap := f.capture(ctx, t, "snap/d1/captures/c1/v2/mem")
	if !f.m.awaitBackingForTest(ctx, "i") {
		t.Fatal("capture identity was not recorded")
	}
	replaceFile(t, f.base, "base-layout-b")
	primed := countHashes(f.m, 0)
	f.m.PrimeBackingDigests(ctx)
	if got := primed.Load(); got != 1 {
		t.Fatalf("primer full reads = %d, want 1 (the replaced base)", got)
	}
	wake := countHashes(f.m, 0)
	if err := f.m.verifySnapshotBacking(ctx, snap, f.base); !errors.Is(err, ErrSnapshotBackingChanged) {
		t.Fatalf("verify = %v, want ErrSnapshotBackingChanged", err)
	}
	if got := wake.Load(); got != 0 {
		t.Fatalf("wake-path full reads after priming = %d, want 0", got)
	}
}

// The primer resolves cached bases without fetching them, and stops
// following a base the cache evicted until a wake attaches it again.
func TestBackingDigestPrimerForgetsEvictedBases(t *testing.T) {
	ctx := context.Background()
	const baseKey = "base/releases/runner-go-amd64-test.ext4"
	dir := t.TempDir()
	kernel := filepath.Join(dir, "vmlinux")
	writeFile(t, kernel, "kernel-v1")
	parent, err := storage.NewLocalStorageBackend(filepath.Join(dir, "store"))
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Put(ctx, baseKey, bytes.NewReader([]byte("base-layout-a"))); err != nil {
		t.Fatal(err)
	}
	cacheDir := filepath.Join(dir, "cache")
	cache, err := storage.NewLocalCacheBackend(uncachedParent{parent}, cacheDir, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(&fakeRunner{}, &fakeVMM{}, Paths{Kernel: kernel}, testFCVersion, nil, nil)
	m.WithStorage(cache)
	m.backingRecorder = newMemoryBackingRecorder()
	if _, err := m.fetchBackingFile(ctx, baseKey); err != nil {
		t.Fatal(err)
	}
	m.noteBackingKey(baseKey)
	hashes := countHashes(m, 0)
	m.PrimeBackingDigests(ctx)
	if got := hashes.Load(); got != 2 {
		t.Fatalf("primer full reads = %d, want kernel and cached base", got)
	}
	cached := storage.CacheFileForKey(cacheDir, baseKey)
	for _, p := range []string{cached, cached + ".meta"} {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	m.PrimeBackingDigests(ctx)
	if got := hashes.Load(); got != 2 {
		t.Fatalf("primer read %d files after the eviction, want no new read", got-2)
	}
	if _, err := m.resolveBackingFile(baseKey); !errors.Is(err, errBackingNotCached) {
		t.Fatalf("primer fetched the evicted base: %v", err)
	}
	m.backingMu.Lock()
	_, following := m.backingKeys[baseKey]
	m.backingMu.Unlock()
	if following {
		t.Fatal("primer still follows the evicted base")
	}
}

// The check's timings reach both breakdowns: the restore's ADR-192 prepare
// timings, and the cold boot that a refusal falls back to.
func TestWakeCarriesBackingCheckTimings(t *testing.T) {
	const delay = 25 * time.Millisecond
	for _, tc := range []struct {
		name       string
		base       string // content renamed over the base before the wake
		wantMethod WakeMethod
	}{
		{name: "accepted restore", base: "base-layout-a", wantMethod: WakeRestore},
		{name: "refusal falls back to cold boot", base: "base-layout-b", wantMethod: WakeColdBoot},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			f := newBackingFixture(ctx, t)
			snap := f.capture(ctx, t, "snap/d1/captures/c1/v2/mem")
			if !f.m.awaitBackingForTest(ctx, "i") {
				t.Fatal("capture identity was not recorded")
			}
			// The verifying Wake hashes a fresh base version on its path.
			replaceFile(t, f.base, tc.base)
			f.vmm.mu.Lock()
			f.vmm.restoreSpecs, f.vmm.coldBootSpecs = nil, nil
			f.vmm.mu.Unlock()
			countHashes(f.m, delay)
			inst, err := f.m.Wake(ctx, WakeRequest{
				Instance: "restore-A", BaseKey: f.base, LayerKey: filepath.Join(f.dir, "layer.ext4"),
				VcpuCount: 2, MemSizeMiB: 128, Plan: api.PlanHobby, Snapshot: snap,
			})
			if err != nil {
				t.Fatalf("Wake: %v", err)
			}
			if inst.Method != tc.wantMethod {
				t.Fatalf("method = %s, want %s", inst.Method, tc.wantMethod)
			}
			f.vmm.mu.Lock()
			defer f.vmm.mu.Unlock()
			var backing BackingCheckTimings
			switch tc.wantMethod {
			case WakeRestore:
				if len(f.vmm.restoreSpecs) != 1 {
					t.Fatalf("restore specs = %d, want 1", len(f.vmm.restoreSpecs))
				}
				backing = f.vmm.restoreSpecs[0].Prepare.Backing
			default:
				if len(f.vmm.coldBootSpecs) != 1 {
					t.Fatalf("cold boot specs = %d, want 1", len(f.vmm.coldBootSpecs))
				}
				backing = f.vmm.coldBootSpecs[0].Backing
			}
			if backing.HashMs < delay.Milliseconds() || backing.TotalMs < backing.HashMs {
				t.Fatalf("backing timings = %+v, want the %s hash attributed", backing, delay)
			}
			if attrs := backing.attrs(); len(attrs) != 8 {
				t.Fatalf("wake ok attrs = %v, want four backing_*_ms fields", attrs)
			}
		})
	}
}
