package builderd

// adr: 20261031103801

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func blobDigest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// writeBlobCache writes a minimal BuildKit local cache holding blobs, by name.
func writeBlobCache(t *testing.T, dir string, blobs map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "blobs", "sha256"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.json"), []byte(`{"schemaVersion":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, content := range blobs {
		if err := os.WriteFile(filepath.Join(dir, "blobs", "sha256", name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func blobInfo(t *testing.T, cache, name string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(filepath.Join(cache, "blobs", "sha256", name))
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func TestParseSharedPlatformBlobs(t *testing.T) {
	for _, tc := range []struct {
		raw     string
		want    bool
		wantErr bool
	}{{"", false, false}, {" ", false, false}, {"1", true, false}, {"true", true, false}, {"false", false, false}, {"yes", false, true}} {
		got, err := ParseSharedPlatformBlobs(tc.raw)
		if got != tc.want || (err != nil) != tc.wantErr {
			t.Errorf("ParseSharedPlatformBlobs(%q) = %v, %v; want %v, error %v", tc.raw, got, err, tc.want, tc.wantErr)
		}
	}
}

// The list belongs to one Railpack version; a bump must bring a new list
// (scripts/railpack-platform-layers.sh).
func TestPlatformLayersMatchRailpackVersion(t *testing.T) {
	for _, tc := range []struct {
		file    string
		pattern string
	}{
		{"../../images/builder-base.Dockerfile", `(?m)^ARG RAILPACK_VERSION=(\S+)$`},
		{"../../.github/workflows/images.yml", `(?m)^\s+RAILPACK_VERSION: "([^"]+)"$`},
	} {
		body, err := os.ReadFile(tc.file)
		if err != nil {
			t.Fatal(err)
		}
		m := regexp.MustCompile(tc.pattern).FindSubmatch(body)
		if m == nil {
			t.Fatalf("%s: no RAILPACK_VERSION", tc.file)
		}
		if got := string(m[1]); got != platformLayersRailpackVersion {
			t.Errorf("%s pins Railpack %s but platformLayers is for %s: regenerate it with scripts/railpack-platform-layers.sh", tc.file, got, platformLayersRailpackVersion)
		}
	}
	for digest := range platformLayers {
		if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(digest) {
			t.Errorf("platform layer %q is not a sha256 hex digest", digest)
		}
	}
}

func TestSharePlatformBlobs(t *testing.T) {
	platform := strings.Repeat("platform layer ", 64)
	own := "the app's own layer"
	forged := blobDigest("another platform layer")
	layers := map[string]struct{}{blobDigest(platform): {}, forged: {}}
	root := t.TempDir()
	first, second := filepath.Join(root, ".publish-1", "cache"), filepath.Join(root, ".publish-2", "cache")
	writeBlobCache(t, first, map[string]string{blobDigest(platform): platform, blobDigest(own): own})
	writeBlobCache(t, second, map[string]string{blobDigest(platform): platform, forged: "not what the name says"})

	if n, bytes := sharePlatformBlobs(root, first, layers); n != 1 || bytes != int64(len(platform)) {
		t.Fatalf("first cache shared %d blobs, %d bytes; want the one platform layer", n, bytes)
	}
	stored, err := os.Stat(filepath.Join(root, platformBlobsDir, "sha256", blobDigest(platform)))
	if err != nil {
		t.Fatalf("no stored copy: %v", err)
	}
	if stored.Mode().Perm() != 0o444 {
		t.Errorf("stored copy mode = %v, want read-only", stored.Mode().Perm())
	}
	if n, _ := sharePlatformBlobs(root, second, layers); n != 1 {
		t.Fatalf("second cache shared %d blobs, want 1", n)
	}
	if !os.SameFile(blobInfo(t, first, blobDigest(platform)), blobInfo(t, second, blobDigest(platform))) {
		t.Error("the caches hold separate copies of the platform layer")
	}
	if !os.SameFile(stored, blobInfo(t, second, blobDigest(platform))) {
		t.Error("the second cache does not link the stored copy")
	}
	if got, _ := os.ReadFile(filepath.Join(second, "blobs", "sha256", forged)); string(got) != "not what the name says" {
		t.Errorf("a listed name with other content changed: %q", got)
	}
	if _, err := os.Stat(filepath.Join(root, platformBlobsDir, "sha256", forged)); !os.IsNotExist(err) {
		t.Errorf("a blob whose content is not its name was stored: %v", err)
	}
	if info := blobInfo(t, first, blobDigest(own)); info.Mode().Perm() != 0o600 {
		t.Errorf("an unlisted blob changed: mode %v", info.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(root, platformBlobsDir, "sha256", blobDigest(own))); !os.IsNotExist(err) {
		t.Errorf("an unlisted blob was stored: %v", err)
	}
}

// A cache that published the stored copy can go; the copy lives while any
// cache links it, and the sweep drops it after the last one.
func TestStoredPlatformBlobOutlivesItsFirstCache(t *testing.T) {
	platform := strings.Repeat("platform layer ", 64)
	layers := map[string]struct{}{blobDigest(platform): {}}
	driveDir := t.TempDir()
	root := filepath.Join(driveDir, devDependencyCacheDir)
	first, second := filepath.Join(root, strings.Repeat("a", 64)), filepath.Join(root, strings.Repeat("b", 64))
	for _, dst := range []string{first, second} {
		src := filepath.Join(t.TempDir(), "export")
		writeBlobCache(t, src, map[string]string{blobDigest(platform): platform})
		shared, err := publishDependencyCache(src, dst, 1<<20, layers)
		if err != nil || shared != int64(len(platform)) {
			t.Fatalf("publish %s: shared %d, %v", filepath.Base(dst), shared, err)
		}
	}
	stored := filepath.Join(root, platformBlobsDir, "sha256", blobDigest(platform))
	if err := os.RemoveAll(first); err != nil {
		t.Fatal(err)
	}
	if err := sweepDependencyCachesWithLimit(driveDir, time.Now(), 1<<30); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(second, "blobs", "sha256", blobDigest(platform))); err != nil || string(got) != platform {
		t.Fatalf("the remaining cache lost its platform layer: %v", err)
	}
	if _, err := os.Stat(stored); err != nil {
		t.Fatalf("the stored copy went while a cache links it: %v", err)
	}
	if err := os.RemoveAll(second); err != nil {
		t.Fatal(err)
	}
	if err := sweepDependencyCachesWithLimit(driveDir, time.Now(), 1<<30); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stored); !os.IsNotExist(err) {
		t.Errorf("the sweep kept a stored copy no cache links: %v", err)
	}
}

// The budget counts a shared layer once, so the sweep evicts only what the
// budget needs. Counted per cache, both caches would be over it.
func TestSweepCountsSharedPlatformLayersOnce(t *testing.T) {
	platform := strings.Repeat("platform layer ", 4096)
	layers := map[string]struct{}{blobDigest(platform): {}}
	driveDir := t.TempDir()
	root := filepath.Join(driveDir, devDependencyCacheDir)
	oldest, newest := filepath.Join(root, strings.Repeat("c", 64)), filepath.Join(root, strings.Repeat("d", 64))
	for i, dst := range []string{oldest, newest} {
		src := filepath.Join(t.TempDir(), "export")
		own := strings.Repeat(string(rune('x'+i)), 100)
		writeBlobCache(t, src, map[string]string{blobDigest(platform): platform, blobDigest(own): own})
		if _, err := publishDependencyCache(src, dst, 1<<20, layers); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	for i, dst := range []string{oldest, newest} {
		stamp := now.Add(time.Duration(i-2) * time.Hour)
		if err := os.Chtimes(dst, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	onDisk, err := dependencyCacheSize(root)
	if err != nil {
		t.Fatal(err)
	}
	index := int64(len(`{"schemaVersion":2}`))
	if want := int64(len(platform)) + 2*(100+index); onDisk != want {
		t.Fatalf("cache root occupies %d bytes, want %d with the platform layer once", onDisk, want)
	}
	if err := sweepDependencyCachesWithLimit(driveDir, now, onDisk); err != nil {
		t.Fatal(err)
	}
	for _, dst := range []string{oldest, newest} {
		if _, err := os.Stat(dst); err != nil {
			t.Errorf("a cache within the budget was evicted: %v", err)
		}
	}
	if err := sweepDependencyCachesWithLimit(driveDir, now, onDisk-1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldest); !os.IsNotExist(err) {
		t.Errorf("the oldest cache survived a budget it exceeds: %v", err)
	}
	if _, err := os.Stat(newest); err != nil {
		t.Errorf("the sweep evicted more than the budget needed: %v", err)
	}
}
