//go:build metal && linux

// adr: 568 — bounded dirty-page acceptance for anonymous capture publication.
package storage

import (
	"crypto/sha256"
	"io"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// Run the compiled test in a disposable cgroup with MemoryMax=256M,
// MemorySwapMax=0 and disk-backed TMPDIR. Dense data prevents sparse holes from
// hiding unbounded dirty file pages in the exclusive publication path.
func TestMetalLargeExclusiveArtifactPublication(t *testing.T) {
	const size = int64(512 << 20)
	backend := exclusiveLocalFixture(t)
	expected := sha256.New()
	reader := io.TeeReader(io.LimitReader(repeatedArtifactReader{}, size), expected)
	key := exclusiveCapturePrefix + "drive"
	if err := PutExclusive(t.Context(), backend, key, reader, size); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(filepath.Join(backend.root, key), os.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	}()
	actual := sha256.New()
	if n, err := io.Copy(actual, file); err != nil || n != size || string(actual.Sum(nil)) != string(expected.Sum(nil)) {
		t.Fatal("bounded exclusive publication changed the original artifact", n, err)
	}
	t.Logf("verified %d-byte exclusive artifact with bounded dirty-page writes", size)
}
