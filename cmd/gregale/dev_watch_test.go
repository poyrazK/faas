package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

func writeDevWatchPackage(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if body != "" {
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestResolveDevWatch(t *testing.T) {
	app := devSourceConfig{shape: shapeApp}
	withDev := writeDevWatchPackage(t, `{"scripts":{"dev":"next dev","build":"next build"}}`)
	withoutDev := writeDevWatchPackage(t, `{"scripts":{"build":"tsc"}}`)
	noPackage := writeDevWatchPackage(t, "")
	for _, tc := range []struct {
		name, dir, command string
		enabled            bool
		want, err          string
	}{
		{name: "off", dir: withDev},
		{name: "dev script default", dir: withDev, enabled: true, want: "npm run dev"},
		{name: "explicit command implies watch", dir: withoutDev, command: "  tsx watch src/index.ts ", want: "tsx watch src/index.ts"},
		{name: "no dev script", dir: withoutDev, enabled: true, err: `"dev" script`},
		{name: "not node", dir: noPackage, enabled: true, err: "package.json"},
		{name: "multi-line command", dir: withDev, command: "npm run dev\nrm -rf /", err: "one line"},
	} {
		got, err := resolveDevWatch(tc.dir, app, tc.enabled, tc.command)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Fatalf("%s: error %v, want %q", tc.name, err, tc.err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if (got == nil) != (tc.want == "") || (got != nil && got.Command != tc.want) {
			t.Fatalf("%s: watch = %+v, want %q", tc.name, got, tc.want)
		}
	}
	if _, err := resolveDevWatch(withDev, devSourceConfig{shape: shapeFunction, runtime: "node22"}, true, ""); err == nil {
		t.Fatal("watch mode was accepted for a function")
	}
}

func TestDevWatchManifestDefault(t *testing.T) {
	manifest := &gregalemanifest.Manifest{Dev: &gregalemanifest.DevConfig{Watch: &gregalemanifest.DevWatchConfig{Enabled: true, Command: "next dev"}}}
	watch, command := false, ""
	applyDevWatchManifestDefault(manifest, map[string]bool{}, &watch, &command)
	if !watch || command != "next dev" {
		t.Fatalf("manifest default = %t %q", watch, command)
	}
	watch, command = false, ""
	applyDevWatchManifestDefault(manifest, map[string]bool{"watch": true}, &watch, &command)
	if watch || command != "" {
		t.Fatal("an explicit --watch flag was overridden by gregale.yaml")
	}
}
