package builderd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatBuildArtifact(t *testing.T) {
	dir := t.TempDir()
	regular := filepath.Join(dir, "image.tar")
	if err := os.WriteFile(regular, []byte("oci artifact"), 0o640); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty.tar")
	if err := os.WriteFile(empty, nil, 0o640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.tar")
	if err := os.Symlink(regular, link); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(dir, "directory.tar")
	if err := os.Mkdir(directory, 0o750); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		path       string
		wantBytes  int64
		wantSubstr string
	}{
		{name: "regular", path: regular, wantBytes: int64(len("oci artifact"))},
		{name: "empty", path: empty, wantSubstr: "empty"},
		{name: "symlink", path: link, wantSubstr: "regular file"},
		{name: "directory", path: directory, wantSubstr: "regular file"},
		{name: "missing", path: filepath.Join(dir, "missing.tar"), wantSubstr: "stat artifact"},
		{name: "blank path", path: "  ", wantSubstr: "path is empty"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := statBuildArtifact(tc.path)
			if tc.wantSubstr == "" {
				if err != nil || got != tc.wantBytes {
					t.Fatalf("statBuildArtifact = (%d, %v), want (%d, nil)", got, err, tc.wantBytes)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("statBuildArtifact error = %v, want substring %q", err, tc.wantSubstr)
			}
		})
	}
}
