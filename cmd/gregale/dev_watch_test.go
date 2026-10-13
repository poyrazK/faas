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
		{name: "no dev script or framework", dir: withoutDev, enabled: true, err: `"dev" script`},
		{name: "not node", dir: noPackage, enabled: true, err: "package.json"},
		{name: "multi-line command", dir: withDev, command: "npm run dev\nrm -rf /", err: "one line"},
	} {
		got, _, err := resolveDevWatch(tc.dir, app, tc.enabled, tc.command)
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
	if _, _, err := resolveDevWatch(withDev, devSourceConfig{shape: shapeFunction, runtime: "node22"}, true, ""); err == nil {
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

// adr: 1170 — phase 3 framework defaults: a dev script that starts a server
// on localhost gets --host 0.0.0.0, a package without one runs its
// framework's development command, and Next.js apps hear how to allow hot
// reload from the environment's hostname.
func TestDevWatchFrameworkDefaults(t *testing.T) {
	for _, tc := range []struct {
		name, pkg, nextConfig string
		want                  string
		note                  string // substring of a note; "" means no notes
	}{
		{name: "vite dev script", pkg: `{"scripts":{"dev":"vite"},"devDependencies":{"vite":"^6"}}`, want: "npm run dev -- --host 0.0.0.0"},
		{name: "vite with flags", pkg: `{"scripts":{"dev":"vite --port 3000"}}`, want: "npm run dev -- --host 0.0.0.0"},
		{name: "env prefix", pkg: `{"scripts":{"dev":"cross-env NODE_ENV=development astro dev"}}`, want: "npm run dev -- --host 0.0.0.0"},
		{name: "sveltekit dev script", pkg: `{"scripts":{"dev":"vite dev"}}`, want: "npm run dev -- --host 0.0.0.0"},
		{name: "nuxt dev script", pkg: `{"scripts":{"dev":"nuxt dev"}}`, want: "npm run dev -- --host 0.0.0.0"},
		{name: "remix vite dev script", pkg: `{"scripts":{"dev":"remix vite:dev"}}`, want: "npm run dev -- --host 0.0.0.0"},
		{name: "react router dev script", pkg: `{"scripts":{"dev":"react-router dev"}}`, want: "npm run dev -- --host 0.0.0.0"},
		{name: "host already set", pkg: `{"scripts":{"dev":"vite --host"}}`, want: "npm run dev"},
		{name: "vite preview is not a dev server", pkg: `{"scripts":{"dev":"vite build && vite preview"}}`, want: "npm run dev"},
		{name: "composite script", pkg: `{"scripts":{"dev":"concurrently \"vite\" \"tsc -w\""}}`, want: "npm run dev", note: "0.0.0.0"},
		{name: "chained script", pkg: `{"scripts":{"dev":"node gen.js && vite"}}`, want: "npm run dev", note: "0.0.0.0"},
		{name: "node watch", pkg: `{"scripts":{"dev":"node --watch index.js"}}`, want: "npm run dev"},
		{name: "next dev script", pkg: `{"scripts":{"dev":"next dev"},"dependencies":{"next":"16.0.0"}}`, want: "npm run dev", note: "allowedDevOrigins"},
		{name: "next configured", pkg: `{"scripts":{"dev":"next dev"},"dependencies":{"next":"16.0.0"}}`,
			nextConfig: "module.exports = { allowedDevOrigins: [process.env.GREGALE_DEV_HOST] }", want: "npm run dev"},
		{name: "next without dev script", pkg: `{"dependencies":{"next":"16.0.0","react":"19.0.0"}}`, want: "next dev", note: "allowedDevOrigins"},
		{name: "nuxt without dev script", pkg: `{"dependencies":{"nuxt":"^4","vue":"^3"}}`, want: "nuxt dev --host 0.0.0.0"},
		{name: "astro without dev script", pkg: `{"dependencies":{"astro":"^5"}}`, want: "astro dev --host 0.0.0.0"},
		{name: "sveltekit without dev script", pkg: `{"devDependencies":{"@sveltejs/kit":"^2","vite":"^6"}}`, want: "vite dev --host 0.0.0.0"},
		{name: "react router without dev script", pkg: `{"devDependencies":{"@react-router/dev":"^7","vite":"^6"}}`, want: "react-router dev --host 0.0.0.0"},
		{name: "remix vite without dev script", pkg: `{"devDependencies":{"@remix-run/dev":"^2","vite":"^5"}}`, want: "remix vite:dev --host 0.0.0.0"},
		{name: "classic remix is not detected", pkg: `{"devDependencies":{"@remix-run/dev":"^2"}}`},
		{name: "vite without dev script", pkg: `{"devDependencies":{"vite":"^6"}}`, want: "vite --host 0.0.0.0"},
		{name: "nothing to run", pkg: `{"dependencies":{"express":"^5"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeDevWatchPackage(t, tc.pkg)
			if tc.nextConfig != "" {
				if err := os.WriteFile(filepath.Join(dir, "next.config.js"), []byte(tc.nextConfig), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			watch, notes, err := resolveDevWatch(dir, devSourceConfig{shape: shapeApp}, true, "")
			if tc.want == "" {
				if err == nil {
					t.Fatalf("watch = %+v, want a request for a dev script", watch)
				}
				if got := devWatchSuggestion(dir); got != "" {
					t.Fatalf("suggestion = %q for a package watch mode cannot run", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if watch.Command != tc.want {
				t.Fatalf("command = %q, want %q", watch.Command, tc.want)
			}
			if got := devWatchSuggestion(dir); got != tc.want {
				t.Fatalf("suggestion = %q, want %q", got, tc.want)
			}
			joined := strings.Join(notes, "\n")
			if (tc.note == "") != (joined == "") || !strings.Contains(joined, tc.note) {
				t.Fatalf("notes = %q, want %q", notes, tc.note)
			}
		})
	}
	// An explicit command is used as written, without framework defaults.
	dir := writeDevWatchPackage(t, `{"scripts":{"dev":"vite"},"dependencies":{"next":"16.0.0"}}`)
	watch, notes, err := resolveDevWatch(dir, devSourceConfig{shape: shapeApp}, false, "vite --host 0.0.0.0 --port 5173")
	if err != nil || watch.Command != "vite --host 0.0.0.0 --port 5173" || len(notes) != 0 {
		t.Fatalf("explicit command = %+v %q %v", watch, notes, err)
	}
}
