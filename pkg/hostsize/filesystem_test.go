//go:build unix

package hostsize

import (
	"path/filepath"
	"testing"
)

func TestFilesystemUsedPct(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{name: "existing directory", path: t.TempDir()},
		{name: "empty path", path: "", wantErr: true},
		{name: "missing path", path: filepath.Join(t.TempDir(), "missing"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pct, err := FilesystemUsedPct(tt.path)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("FilesystemUsedPct(%q) = %v, nil; want an error", tt.path, pct)
				}
				return
			}
			if err != nil {
				t.Fatalf("FilesystemUsedPct(%q): %v", tt.path, err)
			}
			if pct < 0 || pct > 100 {
				t.Fatalf("FilesystemUsedPct(%q) = %v, want a percentage in [0, 100]", tt.path, pct)
			}
		})
	}
}
