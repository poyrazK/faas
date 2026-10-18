package main

import (
	"errors"
	"io"
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
		{name: "no dev script or framework", dir: withoutDev, enabled: true, err: `"dev" script`},
		{name: "nothing detected", dir: noPackage, enabled: true, err: "--watch-command"},
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
	for _, tc := range []struct {
		name        string
		explicit    map[string]bool
		watch       bool
		command     string
		wantWatch   bool
		wantCommand string
	}{
		{name: "manifest default", explicit: map[string]bool{}, wantWatch: true, wantCommand: "next dev"},
		{name: "--watch=false wins", explicit: map[string]bool{"watch": true}},
		// gregale dev --all --watch passes --watch alone; the app's own
		// command still applies.
		{name: "--watch keeps the manifest command", explicit: map[string]bool{"watch": true}, watch: true, wantWatch: true, wantCommand: "next dev"},
		{name: "--watch-command wins", explicit: map[string]bool{"watch-command": true}, command: "vite", wantCommand: "vite"},
	} {
		watch, command := tc.watch, tc.command
		applyDevWatchManifestDefault(manifest, tc.explicit, &watch, &command)
		if watch != tc.wantWatch || command != tc.wantCommand {
			t.Fatalf("%s: = %t %q, want %t %q", tc.name, watch, command, tc.wantWatch, tc.wantCommand)
		}
	}
}

// adr: 1170 — a Next.js app without allowedDevOrigins is offered the edit
// with a terminal and told what to add without one; Django hears about
// ALLOWED_HOSTS.
func TestPrepareDevWatchSource(t *testing.T) {
	next := `{"scripts":{"dev":"next dev"},"dependencies":{"next":"16.4.0"}}`
	t.Run("next accepted", func(t *testing.T) {
		dir := writeDevWatchPackage(t, next)
		var out strings.Builder
		notes := prepareDevWatchSource(dir, true, strings.NewReader("\n"), &out)
		if !strings.Contains(out.String(), "[Y/n]") || len(notes) != 1 || !strings.Contains(notes[0], "added allowedDevOrigins to next.config.mjs") {
			t.Fatalf("prompt %q notes %q", out.String(), notes)
		}
		data, err := os.ReadFile(filepath.Join(dir, "next.config.mjs"))
		if err != nil || !strings.Contains(string(data), "allowedDevOrigins: process.env.GREGALE_DEV_HOST ? [process.env.GREGALE_DEV_HOST] : []") {
			t.Fatalf("next.config.mjs = %q, %v", data, err)
		}
		if again := prepareDevWatchSource(dir, true, strings.NewReader("y\n"), &out); len(again) != 0 {
			t.Fatalf("a configured app was asked again: %q", again)
		}
	})
	t.Run("next declined", func(t *testing.T) {
		dir := writeDevWatchPackage(t, next)
		notes := prepareDevWatchSource(dir, true, strings.NewReader("n\n"), io.Discard)
		if len(notes) != 1 || !strings.Contains(notes[0], "add allowedDevOrigins") {
			t.Fatalf("notes %q", notes)
		}
		if _, err := os.Stat(filepath.Join(dir, "next.config.mjs")); !os.IsNotExist(err) {
			t.Fatal("a declined offer still wrote next.config.mjs")
		}
	})
	t.Run("next without a terminal", func(t *testing.T) {
		dir := writeDevWatchPackage(t, next)
		notes := prepareDevWatchSource(dir, false, strings.NewReader("y\n"), io.Discard)
		if len(notes) != 1 || !strings.Contains(notes[0], "add allowedDevOrigins") {
			t.Fatalf("notes %q", notes)
		}
	})
	t.Run("django", func(t *testing.T) {
		dir := t.TempDir()
		for name, body := range map[string]string{"requirements.txt": "django\n", "manage.py": "", "site/settings.py": "ALLOWED_HOSTS = []\n"} {
			_ = os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755)
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if notes := prepareDevWatchSource(dir, true, strings.NewReader(""), io.Discard); len(notes) != 1 || !strings.Contains(notes[0], "ALLOWED_HOSTS") {
			t.Fatalf("notes %q", notes)
		}
		_ = os.WriteFile(filepath.Join(dir, "site/settings.py"), []byte(`ALLOWED_HOSTS = [os.environ.get("GREGALE_DEV_HOST", "")]`), 0o644)
		if notes := prepareDevWatchSource(dir, true, strings.NewReader(""), io.Discard); len(notes) != 0 {
			t.Fatalf("configured Django still noted: %q", notes)
		}
	})
}

// addNextDevOrigins edits the config shapes create-next-app writes and
// leaves anything else alone.
func TestAddNextDevOrigins(t *testing.T) {
	for _, tc := range []struct {
		name, file, body string
		ok               bool
	}{
		{name: "ts const", file: "next.config.ts", body: "import type { NextConfig } from \"next\";\n\nconst nextConfig: NextConfig = {\n  reactStrictMode: true,\n};\n\nexport default nextConfig;\n", ok: true},
		{name: "js jsdoc const", file: "next.config.js", body: "/** @type {import('next').NextConfig} */\nconst nextConfig = {}\n\nmodule.exports = nextConfig\n", ok: true},
		{name: "module.exports object", file: "next.config.js", body: "module.exports = {\n  output: 'standalone',\n}\n", ok: true},
		{name: "export default object", file: "next.config.mjs", body: "export default {\n  images: { unoptimized: true },\n};\n", ok: true},
		{name: "function config", file: "next.config.js", body: "module.exports = (phase) => ({ reactStrictMode: true })\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, tc.file)
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			name, err := addNextDevOrigins(dir)
			data, _ := os.ReadFile(path)
			if !tc.ok {
				if !errors.Is(err, errNextConfigShape) || string(data) != tc.body {
					t.Fatalf("err %v, file %q; want it untouched", err, data)
				}
				return
			}
			if err != nil || name != tc.file || strings.Count(string(data), "allowedDevOrigins: "+nextDevOriginsExpr+",") != 1 {
				t.Fatalf("%s %v:\n%s", name, err, data)
			}
		})
	}
}
