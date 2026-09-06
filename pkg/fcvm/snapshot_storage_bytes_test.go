package fcvm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAllocatedBytesOrLogicalCountsSparseBlocks(t *testing.T) {
	const logical = int64(64 << 20)
	path := filepath.Join(t.TempDir(), "snapshot.mem")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(make([]byte, 4096)); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Truncate(logical); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	got := allocatedBytesOrLogical(path, logical)
	if got <= 0 || got >= logical {
		t.Fatalf("allocated bytes = %d, want >0 and <%d for sparse file", got, logical)
	}
}

func TestAllocatedBytesOrLogicalFallsBackWhenPathMissing(t *testing.T) {
	const logical = int64(12345)
	if got := allocatedBytesOrLogical(filepath.Join(t.TempDir(), "missing"), logical); got != logical {
		t.Fatalf("allocated bytes = %d, want logical fallback %d", got, logical)
	}
}
