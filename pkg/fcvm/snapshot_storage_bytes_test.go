// adr: 633
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

// TestSnapshotDriveStoredBytesCountsOnlyGuestWrites pins ADR-633: a private
// drive that shares its app layer's blocks adds only the guest's writes to
// the snapshot footprint, and an unmeasured drive keeps the allocated-block
// fallback.
func TestSnapshotDriveStoredBytesCountsOnlyGuestWrites(t *testing.T) {
	const logical = int64(2 << 30)
	if got := snapshotDriveStoredBytes(5<<20, filepath.Join(t.TempDir(), "drive"), logical); got != 5<<20 {
		t.Fatalf("stored = %d, want the guest-written 5 MiB", got)
	}
	if got := snapshotDriveStoredBytes(-1, filepath.Join(t.TempDir(), "missing"), logical); got != logical {
		t.Fatalf("stored = %d, want the logical fallback %d", got, logical)
	}
}
