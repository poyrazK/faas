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

func TestUsedPctMatchesDfUsePercent(t *testing.T) {
	tests := []struct {
		name                string
		blocks, free, avail uint64
		want                float64
		wantErr             bool
	}{
		{name: "empty", blocks: 1000, free: 1000, avail: 1000, want: 0},
		{name: "a third used", blocks: 900, free: 600, avail: 600, want: 100.0 / 3},
		// 5% root reserve: 50 free blocks are not available, so they count
		// as neither used nor available, exactly as df reports Use%.
		{name: "root reserve excluded", blocks: 1000, free: 550, avail: 500, want: 450.0 / 950 * 100},
		{name: "only the reserve left", blocks: 1000, free: 50, avail: 0, want: 100},
		{name: "no usable blocks", blocks: 0, free: 0, avail: 0, wantErr: true},
		{name: "free exceeds total", blocks: 10, free: 11, avail: 11, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := usedPct(tt.blocks, tt.free, tt.avail)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("usedPct(%d, %d, %d) = %v, nil; want an error", tt.blocks, tt.free, tt.avail, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("usedPct(%d, %d, %d): %v", tt.blocks, tt.free, tt.avail, err)
			}
			if diff := got - tt.want; diff > 1e-9 || diff < -1e-9 {
				t.Fatalf("usedPct(%d, %d, %d) = %v, want %v", tt.blocks, tt.free, tt.avail, got, tt.want)
			}
		})
	}
}
