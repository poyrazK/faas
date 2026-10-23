package rootfs

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fuzzLayerSegments are the path pieces a hostile layer combines: traversal,
// absolute names, whiteouts, runtime mountpoints and a sibling of the staging
// directory.
var fuzzLayerSegments = []string{
	"a", "b", "c", "..", ".", "", ".wh.a", ".wh..wh..opq", "x/y", "/abs", "../..", "a/..",
	"outside", "../outside", "/", "proc", "etc", "lib", "bin",
}

// fuzzLayerTypes are the entry kinds applyEntry handles, plus devices.
var fuzzLayerTypes = []byte{tar.TypeReg, tar.TypeDir, tar.TypeSymlink, tar.TypeLink, tar.TypeChar, tar.TypeFifo}

// fuzzLayer decodes data four bytes per entry: kind, two name segments and a
// link target segment.
func fuzzLayer(data []byte) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	seg := func(b byte) string { return fuzzLayerSegments[int(b)%len(fuzzLayerSegments)] }
	for i := 0; i+3 < len(data) && i < 4*64; i += 4 {
		kind := fuzzLayerTypes[int(data[i])%len(fuzzLayerTypes)]
		name := strings.TrimPrefix(seg(data[i+1])+"/"+seg(data[i+2]), "/")
		link := seg(data[i+3])
		if data[i]&0x80 != 0 {
			link = "/" + link
		}
		hdr := &tar.Header{Name: name, Typeflag: kind, Mode: 0o755, Linkname: link}
		body := []byte("x")
		if kind == tar.TypeReg {
			hdr.Size = int64(len(body))
		}
		if tw.WriteHeader(hdr) != nil {
			continue
		}
		if kind == tar.TypeReg {
			_, _ = tw.Write(body)
		}
	}
	_ = tw.Close()
	_ = zw.Close()
	return buf.Bytes()
}

// A layer is tenant data extracted on the host. Whatever it contains, every
// filesystem object it creates must stay inside the staging directory, and a
// sibling directory must be left untouched. Errors are allowed; escapes are
// not.
func FuzzApplyLayerStaysInsideStaging(f *testing.F) {
	for _, seed := range [][]byte{
		{2, 0, 5, 14, 0, 0, 0, 0},        // symlink a -> /, then a file through it
		{2, 0, 5, 0x80 | 13, 0, 0, 1, 0}, // symlink a -> /../outside
		{3, 0, 5, 13, 0, 0, 0, 0},        // hardlink a -> ../outside
		{1, 3, 12, 0, 0, 3, 12, 0},       // ../outside directory and file
		{0, 6, 5, 0, 0, 7, 5, 0},         // whiteouts
		{2, 0, 5, 12, 1, 0, 1, 0, 0, 0, 1, 0},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		root := t.TempDir()
		outside := filepath.Join(root, "outside")
		if err := os.MkdirAll(outside, 0o755); err != nil {
			t.Fatal(err)
		}
		sentinel := filepath.Join(outside, "sentinel")
		if err := os.WriteFile(sentinel, []byte("keep"), 0o644); err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(root, "dst")
		if err := os.MkdirAll(dst, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, apply := range []func(string, *bytes.Reader) error{
			func(d string, r *bytes.Reader) error { return applyLayerGzForApp(d, r, newExtractionBudget(1<<20)) },
			func(d string, r *bytes.Reader) error { return ApplyLayerGzWithOverlayWhiteouts(d, r) },
		} {
			_ = apply(dst, bytes.NewReader(fuzzLayer(data)))
		}
		if got, err := os.ReadFile(sentinel); err != nil || string(got) != "keep" {
			t.Fatalf("sentinel outside staging changed: %q %v", got, err)
		}
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
			if path == root || path == outside || path == sentinel {
				return nil
			}
			// Every path is checked, including one the walk could not read:
			// a fuzzed layer may leave an unreadable directory, and where it
			// sits still matters.
			if rel, relErr := filepath.Rel(dst, path); relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				t.Fatalf("layer created %s outside staging %s", path, dst)
			}
			if walkErr != nil && d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		})
	})
}
