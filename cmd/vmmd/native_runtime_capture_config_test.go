// adr: 20261022184951 — native park/warm capture is an opt-in on a complete native host.
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNativeRuntimeCaptureRequiresCompleteNativeHost(t *testing.T) {
	root := t.TempDir()
	publications := "native_snapshot_publication_root = \"" + filepath.Join(root, "publications") + "\"\n"
	images := "native_image_staging_root = \"" + filepath.Join(root, "images") + "\"\n"
	snapshots := "native_runtime_snapshot_root = \"" + filepath.Join(root, "native-snap") + "\"\n"
	for _, tc := range []struct {
		name     string
		contents string
		want     bool
		wantErr  bool
	}{
		{name: "unset", contents: ""},
		{name: "complete", contents: "native_process_recovery = true\n" + publications + images + snapshots + "native_runtime_capture = true\n", want: true},
		{name: "without native recovery", contents: "native_runtime_capture = true\n", wantErr: true},
		{name: "without publication root", contents: "native_process_recovery = true\n" + images + snapshots + "native_runtime_capture = true\n", wantErr: true},
		{name: "without image staging root", contents: "native_process_recovery = true\n" + publications + snapshots + "native_runtime_capture = true\n", wantErr: true},
		{name: "without native snapshot root", contents: "native_process_recovery = true\n" + publications + images + "native_runtime_capture = true\n", wantErr: true},
		// Restores and collection still need the tree after capture is off.
		{name: "snapshot root kept after capture is off", contents: snapshots},
		{name: "relative snapshot root", contents: "native_runtime_snapshot_root = \"native-snap\"\n", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := filepath.Join(t.TempDir(), "vmmd.toml")
			if err := os.WriteFile(config, []byte(tc.contents), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadConfig(config)
			if (err != nil) != tc.wantErr {
				t.Fatalf("LoadConfig err = %v, want error %v", err, tc.wantErr)
			}
			if err == nil && cfg.NativeRuntimeCapture != tc.want {
				t.Fatalf("NativeRuntimeCapture = %v, want %v", cfg.NativeRuntimeCapture, tc.want)
			}
		})
	}
}
