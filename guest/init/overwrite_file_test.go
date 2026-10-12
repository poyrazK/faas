package main

import (
	"os"
	"path/filepath"
	"testing"
)

// spec: §11 — the post-restore UUID marker must always hold exactly the
// latest UUID, whether the file is new, the same length or a different one.
func TestOverwriteFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uuid.txt")
	for _, content := range []string{
		"6a1f8c2e-3b4d-4e5f-8a9b-0c1d2e3f4a5b\n",                   // created
		"0f9e8d7c-6b5a-4f3e-9d2c-1b0a9f8e7d6c\n",                   // same length, rewritten in place
		"short\n",                                                  // shrinks
		"a much longer marker than any uuid line written so far\n", // grows
	} {
		if err := overwriteFile(path, []byte(content)); err != nil {
			t.Fatalf("overwriteFile(%q): %v", content, err)
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != content {
			t.Fatalf("file = %q (err %v), want %q", got, err, content)
		}
	}
}
