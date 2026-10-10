package crashcrypt

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestCopySparseKeepsContentAndLength(t *testing.T) {
	dir := t.TempDir()
	src := make([]byte, 8<<20)
	copy(src[3<<20:], []byte("data in the middle"))
	src[len(src)-1] = 7
	f, err := os.Create(filepath.Join(dir, "out"))
	if err != nil {
		t.Fatal(err)
	}
	n, err := CopySparse(f, bytes.NewReader(src))
	if err != nil || n != int64(len(src)) {
		t.Fatalf("CopySparse = %d, %v", n, err)
	}
	_ = f.Close()
	got, err := os.ReadFile(filepath.Join(dir, "out"))
	if err != nil || !bytes.Equal(got, src) {
		t.Fatalf("content differs (err=%v)", err)
	}

	// A stream that ends in zeros keeps its full length.
	tail, err := os.Create(filepath.Join(dir, "tail"))
	if err != nil {
		t.Fatal(err)
	}
	if n, err := CopySparse(tail, bytes.NewReader(make([]byte, 200<<10))); err != nil || n != 200<<10 {
		t.Fatalf("zero tail = %d, %v", n, err)
	}
	st, err := tail.Stat()
	_ = tail.Close()
	if err != nil || st.Size() != 200<<10 {
		t.Fatalf("size = %v, %v; want %d", st, err, 200<<10)
	}
}
