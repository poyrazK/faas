package api

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// prod 2026-11-19: every credential got its own cache file and none was ever
// removed (747 files). A write prunes siblings untouched for a week.
func TestCompletionCacheWritePrunesStaleSiblings(t *testing.T) {
	c := newTestCache(t)
	dir := filepath.Dir(c.Path())
	now := time.Now()
	c.now = func() time.Time { return now }
	write := func(name string, age time.Duration) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, now.Add(-age), now.Add(-age)); err != nil {
			t.Fatal(err)
		}
		return path
	}
	stale := write("completion-cache-aaaa.json", 8*24*time.Hour)
	recent := write("completion-cache-bbbb.json", 2*24*time.Hour)
	other := write("config.json", 30*24*time.Hour)

	if err := c.WriteEntry(CompletionCacheEntry{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale cache kept: %v", err)
	}
	for _, kept := range []string{recent, other, c.Path()} {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("%s removed: %v", kept, err)
		}
	}
}
