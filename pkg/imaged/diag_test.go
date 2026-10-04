package imaged

import (
	"context"
	"math"
	"path/filepath"
	"testing"
)

// TestDefaultFcVolumeUsedPct pins the GC pressure probe: a readable volume
// yields a percentage, and an unusable one yields NaN so runGCTick stays out
// of pressure eviction instead of acting on a guessed value.
func TestDefaultFcVolumeUsedPct(t *testing.T) {
	tests := []struct {
		name    string
		root    string
		wantErr bool
	}{
		{name: "existing root", root: t.TempDir()},
		{name: "missing root", root: filepath.Join(t.TempDir(), "missing"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pct, err := DefaultFcVolumeUsedPct(tt.root)(context.Background())
			if tt.wantErr {
				if err == nil || !math.IsNaN(pct) {
					t.Fatalf("DefaultFcVolumeUsedPct(%q) = %v, %v; want NaN and an error", tt.root, pct, err)
				}
				return
			}
			if err != nil || pct < 0 || pct > 100 {
				t.Fatalf("DefaultFcVolumeUsedPct(%q) = %v, %v; want a percentage in [0, 100]", tt.root, pct, err)
			}
		})
	}
}
