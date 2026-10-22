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
	for _, tc := range []struct {
		name     string
		contents string
		want     bool
		wantErr  bool
	}{
		{name: "unset", contents: ""},
		{name: "complete", contents: "native_process_recovery = true\n" + publications + images + "native_runtime_capture = true\n", want: true},
		{name: "without native recovery", contents: "native_runtime_capture = true\n", wantErr: true},
		{name: "without publication root", contents: "native_process_recovery = true\n" + images + "native_runtime_capture = true\n", wantErr: true},
		{name: "without image staging root", contents: "native_process_recovery = true\n" + publications + "native_runtime_capture = true\n", wantErr: true},
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
