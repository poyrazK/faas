package tarball

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func shapeArchive(t *testing.T, headers []*tar.Header, bodies [][]byte) string {
	t.Helper()
	var raw bytes.Buffer
	zr := gzip.NewWriter(&raw)
	tw := tar.NewWriter(zr)
	for i, header := range headers {
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if i < len(bodies) && len(bodies[i]) > 0 {
			if _, err := tw.Write(bodies[i]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zr.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "source.tar.gz")
	if err := os.WriteFile(path, raw.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestValidateShape(t *testing.T) {
	valid := shapeArchive(t, []*tar.Header{{Name: "app/main.go", Typeflag: tar.TypeReg, Mode: 0o600, Size: 1}}, [][]byte{[]byte("x")})
	if err := ValidateShape(valid, 1, 0); err != nil {
		t.Fatalf("valid archive rejected: %v", err)
	}

	empty := shapeArchive(t, nil, nil)
	assertShapeKind(t, ValidateShape(empty, 10, 0), ShapeEmpty)

	escape := shapeArchive(t, []*tar.Header{{Name: "../secret", Typeflag: tar.TypeReg, Mode: 0o600}}, nil)
	assertShapeKind(t, ValidateShape(escape, 10, 0), ShapeUnsafePath)

	link := shapeArchive(t, []*tar.Header{{Name: "link", Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink, Mode: 0o777}}, nil)
	assertShapeKind(t, ValidateShape(link, 10, 0), ShapeUnsafeLink)

	tooMany := shapeArchive(t, []*tar.Header{
		{Name: "one", Typeflag: tar.TypeReg, Mode: 0o600},
		{Name: "two", Typeflag: tar.TypeReg, Mode: 0o600},
	}, nil)
	assertShapeKind(t, ValidateShape(tooMany, 1, 0), ShapeTooMany)

	plain := filepath.Join(t.TempDir(), "plain.tar.gz")
	if err := os.WriteFile(plain, []byte("plain text"), 0o600); err != nil {
		t.Fatal(err)
	}
	assertShapeKind(t, ValidateShape(plain, 10, 0), ShapeNotGzip)
}

func assertShapeKind(t *testing.T, err error, want ShapeErrorKind) {
	t.Helper()
	var shapeErr *ShapeError
	if !errors.As(err, &shapeErr) || shapeErr.Kind != want {
		t.Fatalf("error = %v, want shape kind %q", err, want)
	}
}

// The summed declared entry sizes are capped, so a highly compressed archive
// is refused at its header rather than inflated by every pass over it
// (ADR-20261020160500).
func TestValidateShapeCapsExpandedSize(t *testing.T) {
	file := func(name string, size int) (*tar.Header, []byte) {
		return &tar.Header{Name: name, Mode: 0o644, Size: int64(size), Typeflag: tar.TypeReg}, make([]byte, size)
	}
	h1, b1 := file("a.txt", 600)
	h2, b2 := file("b.txt", 600)
	two := shapeArchive(t, []*tar.Header{h1, h2}, [][]byte{b1, b2})
	if err := ValidateShape(two, 10, 1200); err != nil {
		t.Fatalf("archive exactly at the cap: %v", err)
	}
	assertShapeKind(t, ValidateShape(two, 10, 1000), ShapeTooLarge)

	// 64 MiB of zeros gzips to about 64 KiB.
	hb, bb := file("zeros.bin", 64<<20)
	bomb := shapeArchive(t, []*tar.Header{hb}, [][]byte{bb})
	if info, err := os.Stat(bomb); err != nil || info.Size() > 1<<20 {
		t.Fatalf("bomb archive is %v bytes (%v); the test needs a small compressed file", info.Size(), err)
	}
	assertShapeKind(t, ValidateShape(bomb, 10, 1<<20), ShapeTooLarge)
	if err := ValidateShape(bomb, 10, 0); err != nil {
		t.Fatalf("with the cap off the archive is valid: %v", err)
	}
}
