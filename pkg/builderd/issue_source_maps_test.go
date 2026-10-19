package builderd

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issues"
)

func sourceMapBuildArchive(t *testing.T, root string, configs ...string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "source.tar.gz")
	f, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	write := func(name, body string) {
		t.Helper()
		if err := tw.WriteHeader(&tar.Header{Name: root + name, Mode: 0600, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	write("package.json", "{}")
	for _, cfg := range configs {
		write(api.IssueSourceMapBuildConfigFile, cfg)
	}
	for _, closer := range []interface{ Close() error }{tw, gz, f} {
		if err := closer.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return file
}

func testBuildSourceMapRequest() api.PublishIssueSourceMapsRequest {
	return api.PublishIssueSourceMapsRequest{Directory: "/app/dist", Manifest: api.IssueSourceMapManifest{Version: 1, Artifacts: []api.IssueSourceMapArtifact{{GeneratedFile: "pay.js", SHA256: strings.Repeat("b", 64), Payload: `{"version":3,"sources":["pay.ts"],"names":["charge"],"mappings":"AAAAA"}`}}}}
}

func TestBuildIssueSourceMapConfigFrozenRoot(t *testing.T) {
	valid := `{"version":1,"directory":"/app/dist"}`
	for _, root := range []string{"", "wrapper/", "apps/api/", "wrapper/apps/api/"} {
		t.Run(root, func(t *testing.T) {
			archive := sourceMapBuildArchive(t, root, valid)
			sourceRoot := ""
			if strings.Contains(root, "apps/api") {
				sourceRoot = "apps/api"
			}
			cfg, err := buildIssueSourceMapConfig(archive, sourceRoot)
			if err != nil || cfg == nil || cfg.Directory != "/app/dist" {
				t.Fatalf("selected root lost source-map opt-in: %+v %v", cfg, err)
			}
		})
	}
	for _, configs := range [][]string{nil, {valid, valid}, {valid + `{}`}, {`{"version":1,"directory":"/app/dist","unknown":true}`}, {`{"version":2,"directory":"/app/dist"}`}, {strings.Repeat(" ", api.IssueSourceMapBuildConfigMaxBytes+1)}} {
		archive := sourceMapBuildArchive(t, "", configs...)
		cfg, err := buildIssueSourceMapConfig(archive, "")
		if configs == nil {
			if err != nil || cfg != nil {
				t.Fatal("ordinary sources gained an opt-in", err)
			}
		} else if err == nil {
			t.Fatal("invalid/duplicate config accepted")
		}
	}
}

func TestBuildIssueSourceMapsBoundHandoff(t *testing.T) {
	for _, scenario := range []string{"valid", "wrong-build", "wrong-deployment", "wrong-source", "wrong-image", "wrong-directory", "missing", "symlink", "oversized"} {
		t.Run(scenario, func(t *testing.T) {
			image := filepath.Join(t.TempDir(), "image.tar")
			if err := os.WriteFile(image, []byte("image"), 0600); err != nil {
				t.Fatal(err)
			}
			digest, err := hashFile(image)
			if err != nil {
				t.Fatal(err)
			}
			req := VMRequest{BuildID: "build", DeploymentID: "dep", SourceSHA256: strings.Repeat("a", 64), IssueSourceMaps: &api.IssueSourceMapBuildConfig{Version: 1, Directory: "/app/dist"}}
			handoff := api.BuildIssueSourceMaps{BuildID: req.BuildID, DeploymentID: req.DeploymentID, SourceSHA256: req.SourceSHA256, ImageSHA256: digest, Request: testBuildSourceMapRequest()}
			switch scenario {
			case "wrong-build":
				handoff.BuildID = "other"
			case "wrong-deployment":
				handoff.DeploymentID = "other"
			case "wrong-source":
				handoff.SourceSHA256 = strings.Repeat("c", 64)
			case "wrong-image":
				handoff.ImageSHA256 = strings.Repeat("c", 64)
			case "wrong-directory":
				handoff.Request.Directory = "/app/other"
			}
			data, err := json.Marshal(handoff)
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(filepath.Dir(image), api.BuildIssueSourceMapsFile)
			if scenario != "missing" {
				if err := os.WriteFile(file, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "symlink" {
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("/etc/passwd", file); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "oversized" {
				if err := os.Truncate(file, api.IssueSourceMapBuildMetadataMaxBytes+1); err != nil {
					t.Fatal(err)
				}
			}
			set, got, err := readBuildIssueSourceMaps(image, req)
			if scenario == "valid" {
				if err != nil || set == nil || got != digest {
					t.Fatalf("valid handoff rejected: %v", err)
				}
			} else if err == nil {
				t.Fatal("unbound handoff accepted")
			}
		})
	}
}

func TestBuildSourceMapCacheRequiresBoundMetadata(t *testing.T) {
	image := filepath.Join(t.TempDir(), "image.tar")
	if err := os.WriteFile(image, []byte("image"), 0600); err != nil {
		t.Fatal(err)
	}
	digest, err := hashFile(image)
	if err != nil {
		t.Fatal(err)
	}
	request := testBuildSourceMapRequest()
	set, err := issues.CompileSourceMaps(request)
	if err != nil {
		t.Fatal(err)
	}
	cache := NewCache(t.TempDir())
	recipe := testBuildCacheRecipe("source", FrameworkNode, api.PlanPro, "base")
	oldKey, err := recipe.key()
	if err != nil {
		t.Fatal(err)
	}
	recipe.IssueSourceMapsDirectory = "/app/dist"
	key, err := recipe.key()
	if err != nil || oldKey == key {
		t.Fatal("old builders can satisfy map readiness", err)
	}
	metadata := CacheToolchain{IssueSourceMaps: &set.Request, IssueSourceMapsDigest: set.Digest, IssueSourceMapsImageSHA256: digest}
	if err := cache.StoreBuildWithToolchain(recipe, image, 5, metadata); err != nil {
		t.Fatal(err)
	}
	cached, ok := cache.LookupBuild(recipe)
	if !ok || !validCachedIssueSourceMaps(cached, "/app/dist") {
		t.Fatal("valid cached maps lost")
	}
	for _, scenario := range []string{"missing", "digest", "image", "directory", "modified-map", "unknown-json"} {
		t.Run(scenario, func(t *testing.T) {
			candidate := cached
			candidate.Toolchain = metadata
			switch scenario {
			case "missing":
				candidate.Toolchain.IssueSourceMaps = nil
			case "digest":
				candidate.Toolchain.IssueSourceMapsDigest = strings.Repeat("c", 64)
			case "image":
				candidate.Toolchain.IssueSourceMapsImageSHA256 = strings.Repeat("c", 64)
			case "directory":
				if validCachedIssueSourceMaps(candidate, "/app/other") {
					t.Fatal("wrong directory accepted")
				}
				return
			case "modified-map":
				changed := testBuildSourceMapRequest()
				changed.Manifest.Artifacts[0].Payload = strings.ReplaceAll(changed.Manifest.Artifacts[0].Payload, "pay.ts", "other.ts")
				candidate.Toolchain.IssueSourceMaps = &changed
			case "unknown-json":
				file := filepath.Join(filepath.Dir(cached.Path), "toolchain.json")
				data, err := json.Marshal(metadata)
				if err != nil {
					t.Fatal(err)
				}
				data = append(data[:len(data)-1], []byte(`,"unexpected":true}`)...)
				if err := os.WriteFile(file, data, 0600); err != nil {
					t.Fatal(err)
				}
				candidate, _ = cache.LookupBuild(recipe)
			}
			if validCachedIssueSourceMaps(candidate, "/app/dist") {
				t.Fatal("unbound cached maps accepted")
			}
		})
	}
}
