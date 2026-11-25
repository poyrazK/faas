package storage

// adr: 20261115213230 — a snapshot drive is uploaded as the blocks that
// differ from its app layer and rebuilt byte for byte on fetch.

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

const (
	testLayerKey = "layers/dep-1.ext4"
	testDriveKey = "snap/dep-1/captures/cap-1/v2/drive"
)

// writeSparse writes a size-byte file at path holding blocks (block index →
// content) and holes everywhere else.
func writeSparse(t *testing.T, path string, size int64, blocks map[int64][]byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	for b, data := range blocks {
		if b*driveDeltaBlock >= size {
			continue
		}
		if _, err := f.WriteAt(data, b*driveDeltaBlock); err != nil {
			t.Fatal(err)
		}
	}
}

func randomBlock(r *rand.Rand) []byte {
	b := make([]byte, driveDeltaBlock)
	_, _ = r.Read(b)
	return b
}

// layerAndDrive returns a layer and a drive derived from it: some blocks
// rewritten, one zeroed (a discard), some new, and a different length.
func layerAndDrive(t *testing.T, dir string, driveSize int64) (layer, drive string) {
	t.Helper()
	r := rand.New(rand.NewSource(1))
	base := map[int64][]byte{}
	for _, b := range []int64{0, 1, 2, 3, 10, 11, 12, 200, 201, 450} {
		base[b] = randomBlock(r)
	}
	derived := map[int64][]byte{}
	for b, data := range base {
		derived[b] = data
	}
	derived[2] = randomBlock(r)                   // rewritten
	derived[3] = randomBlock(r)                   // rewritten, adjacent: one record
	derived[11] = make([]byte, driveDeltaBlock)   // discarded
	derived[520] = randomBlock(r)                 // new file data
	derived[201] = append([]byte{}, base[201]...) // unchanged copy
	derived[201][17] ^= 0xff                      // one byte changed
	layer = filepath.Join(dir, "layer.ext4")
	drive = filepath.Join(dir, "drive.ext4")
	writeSparse(t, layer, 600*driveDeltaBlock+1234, base)
	writeSparse(t, drive, driveSize, derived)
	return layer, drive
}

func readAll(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:forbidigo // test fixture.
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDriveDeltaRoundTrip(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		size int64
	}{
		{"same size", 600*driveDeltaBlock + 1234},
		{"grown", 900 * driveDeltaBlock},
		{"shrunk", 300*driveDeltaBlock + 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			layerPath, drivePath := layerAndDrive(t, dir, tc.size)
			layer, _ := os.Open(layerPath) //nolint:forbidigo // test fixture.
			defer func() { _ = layer.Close() }()
			drive, _ := os.Open(drivePath) //nolint:forbidigo // test fixture.
			defer func() { _ = drive.Close() }()

			var payload bytes.Buffer
			stats, err := encodeDriveDelta(ctx, &payload, drive, layer, testLayerKey)
			if err != nil {
				t.Fatal(err)
			}
			if stats.DataBlocks == 0 || stats.DataBlocks > 6 {
				t.Fatalf("data blocks = %d, want the few changed ones", stats.DataBlocks)
			}
			if payload.Len() > 8*driveDeltaBlock {
				t.Fatalf("delta is %d bytes for a handful of changed blocks", payload.Len())
			}

			out, err := os.Create(filepath.Join(dir, "rebuilt"))
			if err != nil {
				t.Fatal(err)
			}
			src := readAll(t, layerPath)
			if _, err := out.Write(src); err != nil {
				t.Fatal(err)
			}
			br := bufio.NewReader(&payload)
			h, err := readDriveDeltaHeader(br)
			if err != nil {
				t.Fatal(err)
			}
			if h.baseKey != testLayerKey || h.size != tc.size {
				t.Fatalf("header = %+v", h)
			}
			if err := applyDriveDelta(ctx, br, h, out); err != nil {
				t.Fatal(err)
			}
			_ = out.Close()
			if !bytes.Equal(readAll(t, out.Name()), readAll(t, drivePath)) {
				t.Fatal("rebuilt drive differs from the drive")
			}
		})
	}
}

func TestDriveDataDigestIgnoresLayout(t *testing.T) {
	dir := t.TempDir()
	r := rand.New(rand.NewSource(2))
	blk := randomBlock(r)
	sparse := filepath.Join(dir, "sparse")
	writeSparse(t, sparse, 64*driveDeltaBlock, map[int64][]byte{5: blk})
	dense := filepath.Join(dir, "dense")
	full := make([]byte, 64*driveDeltaBlock)
	copy(full[5*driveDeltaBlock:], blk)
	if err := os.WriteFile(dense, full, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := func(p string) [32]byte {
		f, _ := os.Open(p) //nolint:forbidigo // test fixture.
		defer func() { _ = f.Close() }()
		d, err := driveDataDigest(context.Background(), f, 64*driveDeltaBlock)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	if digest(sparse) != digest(dense) {
		t.Fatal("equal bytes with different holes digest differently")
	}
	full[5*driveDeltaBlock] ^= 1
	if err := os.WriteFile(dense, full, 0o600); err != nil {
		t.Fatal(err)
	}
	if digest(sparse) == digest(dense) {
		t.Fatal("a changed byte did not change the digest")
	}
}

func TestReadDriveDeltaHeaderRejectsBadInput(t *testing.T) {
	for name, payload := range map[string]string{
		"no magic":     "ext4 image bytes",
		"bad version":  driveDeltaMagic + "\x02",
		"empty key":    driveDeltaMagic + "\x01\x00",
		"invalid key":  driveDeltaMagic + "\x01\x05../..",
		"short digest": driveDeltaMagic + "\x01\x11layers/dep-1.ext4\x10abc",
	} {
		if _, err := readDriveDeltaHeader(bufio.NewReader(bytes.NewBufferString(payload))); err == nil {
			t.Errorf("%s: header accepted", name)
		}
	}
}

// hiddenParent is a local backend without LocalPath, as a remote parent is,
// so the cache must hold its own copies.
type hiddenParent struct{ b *LocalStorageBackend }

func (p hiddenParent) Put(ctx context.Context, key string, r io.Reader) error {
	return p.b.Put(ctx, key, r)
}
func (p hiddenParent) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	return p.b.Get(ctx, key)
}
func (p hiddenParent) Delete(ctx context.Context, key string) error { return p.b.Delete(ctx, key) }
func (p hiddenParent) Exists(ctx context.Context, key string) (bool, error) {
	return p.b.Exists(ctx, key)
}
func (p hiddenParent) List(ctx context.Context, prefix string) ([]string, error) {
	return p.b.List(ctx, prefix)
}

func newDeltaCache(t *testing.T, dir string, deltas bool) (*LocalCacheBackend, *LocalStorageBackend) {
	t.Helper()
	shared, err := NewLocalStorageBackend(filepath.Join(dir, "shared"))
	if err != nil {
		t.Fatal(err)
	}
	cache, err := NewLocalCacheBackend(hiddenParent{shared}, filepath.Join(dir, "cache"), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	cache.SetDriveDeltas(deltas)
	return cache, shared
}

func putDeltaFile(t *testing.T, c *LocalCacheBackend, key, path, baseKey string) {
	t.Helper()
	f, err := os.Open(path) //nolint:forbidigo // test fixture.
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := c.Put(context.Background(), key, WithDriveDeltaBase(f, baseKey)); err != nil {
		t.Fatal(err)
	}
}

func parentBytes(t *testing.T, shared *LocalStorageBackend, key string) []byte {
	t.Helper()
	rc, err := shared.Get(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func getBytes(t *testing.T, c *LocalCacheBackend, key string) ([]byte, error) {
	t.Helper()
	rc, err := c.Get(context.Background(), key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(rc)
}

func TestCacheUploadsDriveAsDeltaAndRebuildsIt(t *testing.T) {
	dir := t.TempDir()
	layerPath, drivePath := layerAndDrive(t, dir, 600*driveDeltaBlock+1234)
	cache, shared := newDeltaCache(t, dir, true)
	putDeltaFile(t, cache, testLayerKey, layerPath, "")
	putDeltaFile(t, cache, testDriveKey, drivePath, testLayerKey)

	uploaded := parentBytes(t, shared, testDriveKey)
	if !bytes.HasPrefix(uploaded, []byte(driveDeltaMagic)) {
		t.Fatal("the parent holds the whole drive, not a delta")
	}
	if len(uploaded) > 8*driveDeltaBlock {
		t.Fatalf("uploaded %d bytes for a handful of changed blocks", len(uploaded))
	}
	want := readAll(t, drivePath)
	// The cache entry written at Put is the whole drive.
	if got, err := getBytes(t, cache, testDriveKey); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("cached drive differs (err %v)", err)
	}

	// A node that never saw the drive or the layer rebuilds both from the
	// parent.
	other, err := NewLocalCacheBackend(hiddenParent{shared}, filepath.Join(dir, "other-cache"), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := getBytes(t, other, testDriveKey); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("drive rebuilt on another node differs (err %v)", err)
	}
	if _, local, _ := other.LocalPath(testLayerKey); !local {
		t.Fatal("rebuilding the drive did not cache its layer")
	}

	// And after an eviction on the first node.
	if err := cache.EvictLocal(testDriveKey); err != nil {
		t.Fatal(err)
	}
	if got, err := getBytes(t, cache, testDriveKey); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("drive rebuilt after eviction differs (err %v)", err)
	}
}

func TestCacheRefusesADeltaWhoseLayerChanged(t *testing.T) {
	dir := t.TempDir()
	layerPath, drivePath := layerAndDrive(t, dir, 600*driveDeltaBlock+1234)
	cache, shared := newDeltaCache(t, dir, true)
	putDeltaFile(t, cache, testLayerKey, layerPath, "")
	putDeltaFile(t, cache, testDriveKey, drivePath, testLayerKey)

	changed := readAll(t, layerPath)
	changed[0] ^= 1
	if err := shared.Put(context.Background(), testLayerKey, bytes.NewReader(changed)); err != nil {
		t.Fatal(err)
	}
	other, err := NewLocalCacheBackend(hiddenParent{shared}, filepath.Join(dir, "other-cache"), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := getBytes(t, other, testDriveKey); !errors.Is(err, errDriveDeltaBaseChanged) {
		t.Fatalf("Get = %v, want errDriveDeltaBaseChanged", err)
	}
	if _, local, _ := other.LocalPath(testDriveKey); local {
		t.Fatal("a drive rebuilt against the wrong layer was cached")
	}
}

func TestCacheUploadsWholeDriveWhenDeltasAreOffOrTheLayerIsAbsent(t *testing.T) {
	for _, tc := range []struct {
		name        string
		deltas      bool
		cacheLayer  bool
		wantPartial bool
	}{
		{"deltas off", false, true, false},
		{"layer not on this node", true, false, false},
		{"deltas on", true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			layerPath, drivePath := layerAndDrive(t, dir, 600*driveDeltaBlock+1234)
			cache, shared := newDeltaCache(t, dir, tc.deltas)
			if tc.cacheLayer {
				putDeltaFile(t, cache, testLayerKey, layerPath, "")
			}
			putDeltaFile(t, cache, testDriveKey, drivePath, testLayerKey)
			isDelta := bytes.HasPrefix(parentBytes(t, shared, testDriveKey), []byte(driveDeltaMagic))
			if isDelta != tc.wantPartial {
				t.Fatalf("parent holds a delta = %v, want %v", isDelta, tc.wantPartial)
			}
		})
	}
}

// Any other key, even with a base, is stored whole.
func TestCacheIgnoresDeltaBaseOutsideSnapshotDrives(t *testing.T) {
	dir := t.TempDir()
	layerPath, drivePath := layerAndDrive(t, dir, 600*driveDeltaBlock+1234)
	cache, shared := newDeltaCache(t, dir, true)
	putDeltaFile(t, cache, testLayerKey, layerPath, "")
	putDeltaFile(t, cache, "layers/dep-2.ext4", drivePath, testLayerKey)
	if !bytes.Equal(parentBytes(t, shared, "layers/dep-2.ext4"), readAll(t, drivePath)) {
		t.Fatal("a layer was uploaded as a delta")
	}
}

// A local parent (one-box storage) hands out its own files by path. For a
// snapshot drive uploaded as a delta that file is the encoded payload, and a
// VM restored from it booted a 1.6 MB "disk" (desktop metal test,
// 2026-11-24). LocalPath and Get must give the whole drive.
func TestCacheNeverHandsOutALocalParentsDelta(t *testing.T) {
	dir := t.TempDir()
	layerPath, drivePath := layerAndDrive(t, dir, 600*driveDeltaBlock+1234)
	shared, err := NewLocalStorageBackend(filepath.Join(dir, "shared"))
	if err != nil {
		t.Fatal(err)
	}
	cache, err := NewLocalCacheBackend(shared, filepath.Join(dir, "cache"), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	cache.SetDriveDeltas(true)
	putDeltaFile(t, cache, testLayerKey, layerPath, "")
	putDeltaFile(t, cache, testDriveKey, drivePath, testLayerKey)
	if !bytes.HasPrefix(parentBytes(t, shared, testDriveKey), []byte(driveDeltaMagic)) {
		t.Fatal("the parent holds the whole drive, not a delta")
	}
	other, err := NewLocalCacheBackend(shared, filepath.Join(dir, "other-cache"), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	// A cold node, before any Get, has only the parent's delta: nothing may
	// report it as the drive on this node.
	if path, local, err := other.LocalPath(testDriveKey); err != nil || local {
		t.Fatalf("cold node: LocalPath = %q %v %v, want not local", path, local, err)
	}
	if path, local, _ := LocalPathWithoutTouch(other, testDriveKey); local {
		t.Fatalf("cold node: LocalPathWithoutTouch = %q, want not local", path)
	}
	if cached, _, err := CachedLocally(other, testDriveKey); err != nil || cached {
		t.Fatalf("cold node: CachedLocally = %v %v, want false", cached, err)
	}
	if err := other.MarkGeneration(testDriveKey, "gen-1"); err == nil {
		t.Fatal("cold node: MarkGeneration took the parent's delta for the drive")
	}
	want := readAll(t, drivePath)
	for name, c := range map[string]*LocalCacheBackend{"capturing node": cache, "another node": other} {
		if got, err := getBytes(t, c, testDriveKey); err != nil || !bytes.Equal(got, want) {
			t.Fatalf("%s: Get differs from the drive (err %v)", name, err)
		}
		path, local, err := c.LocalPath(testDriveKey)
		if err != nil || !local {
			t.Fatalf("%s: LocalPath = %q %v %v", name, path, local, err)
		}
		if !bytes.Equal(readAll(t, path), want) {
			t.Fatalf("%s: LocalPath %s is not the drive", name, path)
		}
		if path, local, _ := LocalPathWithoutTouch(c, testDriveKey); !local || !bytes.Equal(readAll(t, path), want) {
			t.Fatalf("%s: LocalPathWithoutTouch %s is not the drive", name, path)
		}
	}
}

// isDriveDeltaPayload fails closed: a snapshot drive file it cannot read
// counts as a delta, so the cache never hands it out unchecked.
func TestIsDriveDeltaPayload(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string, mode os.FileMode) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		return path
	}
	delta := write("delta", driveDeltaMagic+"rest", 0o600)
	drive := write("drive", "an ext4 image, not a delta", 0o600)
	short := write("short", "ab", 0o600)
	unreadable := write("unreadable", "an ext4 image", 0o000)
	for _, tc := range []struct {
		name, key, path string
		want            bool
	}{
		{"a delta", testDriveKey, delta, true},
		{"a whole drive", testDriveKey, drive, false},
		{"a short file", testDriveKey, short, false},
		{"a missing file", testDriveKey, filepath.Join(dir, "missing"), true},
		{"another key", testLayerKey, delta, false},
	} {
		if got := isDriveDeltaPayload(tc.key, tc.path); got != tc.want {
			t.Errorf("%s: isDriveDeltaPayload = %v, want %v", tc.name, got, tc.want)
		}
	}
	if os.Geteuid() != 0 && !isDriveDeltaPayload(testDriveKey, unreadable) {
		t.Error("an unreadable drive file is handed out as the drive")
	}
}
