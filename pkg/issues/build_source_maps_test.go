package issues

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

const buildMapFixture = `{"version":3,"sourceRoot":"../","sources":["src/pay.ts"],"names":["charge"],"mappings":"AASAA","sourcesContent":["PRIVATE_SOURCE"],"producerSecret":"PRIVATE_EXTENSION"}`

func buildMapTree(t *testing.T, files map[string]string) (*os.Root, string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		file := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root, dir
}

func TestDiscoverBuildSourceMapsFinalBytesAndSanitization(t *testing.T) {
	bundle := "throw new Error('failed');\n//# sourceMappingURL=maps/pay.js.map\n"
	root, _ := buildMapTree(t, map[string]string{"app/dist/pay.js": bundle, "app/dist/maps/pay.js.map": buildMapFixture, "app/dist/z.cjs": "hidden", "app/dist/z.cjs.map": buildMapFixture, "app/dist/node_modules/ignored.js": "ignore", "app/dist/without.js": "no map"})
	cfg := api.IssueSourceMapBuildConfig{Version: 1, Directory: "/app/dist"}
	set, err := DiscoverBuildSourceMaps(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Request.Manifest.Artifacts) != 2 || set.Request.Manifest.Artifacts[0].GeneratedFile != "pay.js" {
		t.Fatalf("unexpected discovery: %+v", set.Request)
	}
	digest := sha256.Sum256([]byte(bundle))
	if set.Request.Manifest.Artifacts[0].SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatal("hash does not describe final bundle bytes")
	}
	raw, _ := json.Marshal(set.Request)
	if strings.Contains(string(raw), "PRIVATE_") || strings.Contains(string(raw), "sourcesContent") {
		t.Fatal("private source contents escaped sanitization")
	}
	mapped, counts := set.Symbolicate(api.IssueEvent{Frames: []api.IssueFrame{{File: "/app/dist/pay.js", Line: 1, Column: 1, InApp: true}}})
	if counts.MappedFrames != 1 || mapped.Frames[0].File != "/app/dist/src/pay.ts" || mapped.Frames[0].Line != 10 || mapped.Frames[0].Function != "charge" {
		t.Fatalf("map URL rebasing lost original location: %+v", mapped.Frames)
	}
	again, err := DiscoverBuildSourceMaps(root, cfg)
	if err != nil || again.Digest != set.Digest {
		t.Fatal("discovery is not deterministic", err)
	}
}

func TestDiscoverBuildSourceMapsRefusesUnsafeOrMissingArtifacts(t *testing.T) {
	for _, scenario := range []string{"empty", "missing-reference", "remote", "inline", "absolute", "query", "fragment", "escape", "encoded-escape", "map-symlink", "map-fifo", "directory-symlink", "indexed", "absolute-source", "oversized-bundle", "oversized-map", "too-many-maps", "too-many-entries"} {
		t.Run(scenario, func(t *testing.T) {
			files := map[string]string{"app/dist/pay.js": "bundle", "app/dist/pay.js.map": buildMapFixture}
			refs := map[string]string{"missing-reference": "missing.map", "remote": "https://example.com/pay.map", "inline": "data:application/json,abc", "absolute": "/tmp/pay.map", "query": "pay.js.map?secret=x", "fragment": "pay.js.map#x", "escape": "../../private.map", "encoded-escape": "%2e%2e/%2e%2e/private.map"}
			if ref, ok := refs[scenario]; ok {
				files["app/dist/pay.js"] += "\n//# sourceMappingURL=" + ref
			}
			if scenario == "empty" {
				delete(files, "app/dist/pay.js.map")
			}
			if scenario == "indexed" {
				files["app/dist/pay.js.map"] = `{"version":3,"sections":[],"sources":[],"names":[],"mappings":""}`
			}
			if scenario == "absolute-source" {
				files["app/dist/pay.js.map"] = strings.ReplaceAll(buildMapFixture, "src/pay.ts", "/private/pay.ts")
			}
			if scenario == "too-many-maps" {
				for i := 0; i < api.IssueSourceMapsMax; i++ {
					file := fmt.Sprintf("app/dist/chunk%02d.js", i)
					files[file], files[file+".map"] = "chunk", buildMapFixture
				}
			}
			if scenario == "too-many-entries" {
				for i := 0; i < api.IssueSourceMapDiscoveryEntriesMax; i++ {
					files[fmt.Sprintf("app/dist/file%04d.txt", i)] = ""
				}
			}
			root, dir := buildMapTree(t, files)
			switch scenario {
			case "map-symlink", "map-fifo":
				file := filepath.Join(dir, "app/dist/pay.js.map")
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
				var err error
				if scenario == "map-symlink" {
					err = os.Symlink("/etc/passwd", file)
				} else {
					err = syscall.Mkfifo(file, 0600)
				}
				if err != nil {
					t.Fatal(err)
				}
			case "directory-symlink":
				if err := os.Rename(filepath.Join(dir, "app/dist"), filepath.Join(dir, "app/real")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("real", filepath.Join(dir, "app/dist")); err != nil {
					t.Fatal(err)
				}
			case "oversized-bundle", "oversized-map":
				name, size := "app/dist/pay.js", int64(api.IssueSourceMapBundleMaxBytes+1)
				if scenario == "oversized-map" {
					name, size = "app/dist/pay.js.map", api.IssueSourceMapMaxBytes+1
				}
				if err := os.Truncate(filepath.Join(dir, name), size); err != nil {
					t.Fatal(err)
				}
			}
			_, err := DiscoverBuildSourceMaps(root, api.IssueSourceMapBuildConfig{Version: 1, Directory: "/app/dist"})
			if err == nil {
				t.Fatal("unsafe or missing artifacts accepted")
			}
			if strings.Contains(err.Error(), "PRIVATE_") {
				t.Fatal("private map contents leaked in error")
			}
		})
	}
}

func TestBuildSourceMapConfigValidation(t *testing.T) {
	for _, dir := range []string{"/", "/tmp", "/application", "/app/../etc", "/app//dist", "/app/dist/", "app/dist", "/app/a\\b", "/app/a\x00b"} {
		if ValidateSourceMapBuildConfig(api.IssueSourceMapBuildConfig{Version: 1, Directory: dir}) == nil {
			t.Fatalf("accepted directory %q", dir)
		}
	}
	if ValidateSourceMapBuildConfig(api.IssueSourceMapBuildConfig{Version: 2, Directory: "/app/dist"}) == nil {
		t.Fatal("accepted unsupported version")
	}
}
