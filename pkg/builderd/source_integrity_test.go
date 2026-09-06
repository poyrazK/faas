package builderd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHashAndVerifySource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.tar.gz")
	if err := os.WriteFile(path, []byte("source-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	want, err := hashFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if got, err := hashAndVerifySource(path, want); err != nil || got != want {
		t.Fatalf("matching digest: got %q err %v, want %q", got, err, want)
	}
	if got, err := hashAndVerifySource(path, ""); err != nil || got != want {
		t.Fatalf("legacy empty digest: got %q err %v, want %q", got, err, want)
	}
	if _, err := hashAndVerifySource(path, strings.Repeat("0", len(want))); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("mismatched digest error = %v", err)
	}
}
