package builderd

import (
	"github.com/onebox-faas/faas/pkg/api"
	"os"
	"path/filepath"
	"testing"
)

func TestCacheCorruptionIsMissAndRepairable(t *testing.T) {
	for _, kind := range []string{"empty", "same-size", "missing-digest", "wrong-source"} {
		t.Run(kind, func(t *testing.T) {
			c := NewCache(t.TempDir())
			src := filepath.Join(t.TempDir(), "image.tar")
			original := []byte("known artifact bytes")
			if err := os.WriteFile(src, original, 0600); err != nil {
				t.Fatal(err)
			}
			if err := c.Store("source", FrameworkNode, api.PlanHobby, src, int64(len(original))); err != nil {
				t.Fatal(err)
			}
			entry, ok := c.Lookup("source", FrameworkNode, api.PlanHobby)
			if !ok {
				t.Fatal("initial miss")
			}
			var err error
			switch kind {
			case "empty":
				err = os.Truncate(entry.Path, 0)
			case "same-size":
				err = os.WriteFile(entry.Path, []byte("other artifact bytes"), 0600)
			case "missing-digest":
				err = os.Remove(filepath.Join(filepath.Dir(entry.Path), "artifact.sha256"))
			case "wrong-source":
				err = os.WriteFile(c.checksumPath("source", FrameworkNode, api.PlanHobby), []byte("wrong"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := c.Lookup("source", FrameworkNode, api.PlanHobby); ok {
				t.Fatal("corrupt cache hit")
			}
			if err := c.Store("source", FrameworkNode, api.PlanHobby, src, int64(len(original))); err != nil {
				t.Fatal(err)
			}
			repaired, ok := c.Lookup("source", FrameworkNode, api.PlanHobby)
			if !ok {
				t.Fatal("entry not repaired")
			}
			data, err := os.ReadFile(repaired.Path)
			if err != nil || string(data) != string(original) {
				t.Fatalf("repair %q: %v", data, err)
			}
		})
	}
}
