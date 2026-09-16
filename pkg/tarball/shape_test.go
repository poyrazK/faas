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
	if err := ValidateShape(valid, 1); err != nil {
		t.Fatalf("valid archive rejected: %v", err)
	}

	empty := shapeArchive(t, nil, nil)
	assertShapeKind(t, ValidateShape(empty, 10), ShapeEmpty)

	escape := shapeArchive(t, []*tar.Header{{Name: "../secret", Typeflag: tar.TypeReg, Mode: 0o600}}, nil)
	assertShapeKind(t, ValidateShape(escape, 10), ShapeUnsafePath)

	link := shapeArchive(t, []*tar.Header{{Name: "link", Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink, Mode: 0o777}}, nil)
	assertShapeKind(t, ValidateShape(link, 10), ShapeUnsafeLink)

	tooMany := shapeArchive(t, []*tar.Header{
		{Name: "one", Typeflag: tar.TypeReg, Mode: 0o600},
		{Name: "two", Typeflag: tar.TypeReg, Mode: 0o600},
	}, nil)
	assertShapeKind(t, ValidateShape(tooMany, 1), ShapeTooMany)

	plain := filepath.Join(t.TempDir(), "plain.tar.gz")
	if err := os.WriteFile(plain, []byte("plain text"), 0o600); err != nil {
		t.Fatal(err)
	}
	assertShapeKind(t, ValidateShape(plain, 10), ShapeNotGzip)
}

func assertShapeKind(t *testing.T, err error, want ShapeErrorKind) {
	t.Helper()
	var shapeErr *ShapeError
	if !errors.As(err, &shapeErr) || shapeErr.Kind != want {
		t.Fatalf("error = %v, want shape kind %q", err, want)
	}
}
