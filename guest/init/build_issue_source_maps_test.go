//go:build linux

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestBuildIssueSourceMapsExportFinalFilesystem(t *testing.T) {
	root, out := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "app/dist"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string]string{"pay.js": "bundle", "pay.js.map": `{"version":3,"sources":["pay.ts"],"names":["charge"],"mappings":"AAAAA","sourcesContent":["PRIVATE_SOURCE"]}`} {
		if err := os.WriteFile(filepath.Join(root, "app/dist", name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	image := []byte("final image fixture")
	if err := os.WriteFile(filepath.Join(out, "image.tar"), image, 0600); err != nil {
		t.Fatal(err)
	}
	m := api.BuildManifest{BuildID: "build", DeploymentID: "deployment", SourceSHA256: strings.Repeat("a", 64), OutDir: out, IssueSourceMaps: &api.IssueSourceMapBuildConfig{Version: 1, Directory: "/app/dist"}}
	if err := writeBuildIssueSourceMaps(m, root); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(out, api.BuildIssueSourceMapsFile)
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var handoff api.BuildIssueSourceMaps
	if err := json.Unmarshal(data, &handoff); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(image)
	if handoff.BuildID != m.BuildID || handoff.DeploymentID != m.DeploymentID || handoff.SourceSHA256 != m.SourceSHA256 || handoff.ImageSHA256 != hex.EncodeToString(digest[:]) || len(handoff.Request.Manifest.Artifacts) != 1 {
		t.Fatalf("incomplete final-build binding: %+v", handoff)
	}
	st, err := os.Stat(file)
	if err != nil || st.Mode().Perm() != 0600 || strings.Contains(string(data), "PRIVATE_SOURCE") {
		t.Fatal("source-map handoff is not private and sanitized", err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "app/dist/pay.js.map")); err != nil {
		t.Fatal(err)
	}
	if err := writeBuildIssueSourceMaps(m, root); err == nil {
		t.Fatal("empty discovery reported success")
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("failed discovery published a handoff")
	}
}

func TestBuildArgvIssueSourceMapsSameFinalResult(t *testing.T) {
	for _, cache := range []bool{false, true} {
		m := api.BuildManifest{Framework: api.FrameworkRailpackNode, Workdir: "/build/src", OutDir: "/build/out", DependencyCache: cache}
		ordinary := strings.Join(buildArgv(m), " ")
		if strings.Contains(ordinary, buildIssueSourceMapRootfs) {
			t.Fatal("ordinary builds gained a local exporter")
		}
		m.IssueSourceMaps = &api.IssueSourceMapBuildConfig{Version: 1, Directory: "/app/dist"}
		opted := strings.Join(buildArgv(m), " ")
		if strings.Count(opted, "buildctl") != 1 || strings.Count(opted, "--output") != 2 || !strings.Contains(opted, "type=oci,dest=") || !strings.Contains(opted, "type=local,dest="+buildIssueSourceMapRootfs) {
			t.Fatalf("exporters do not share one BuildKit result: %s", opted)
		}
	}
}

func TestBuildIssueSourceMapsRefusesUnsafeImage(t *testing.T) {
	for _, scenario := range []string{"symlink", "fifo", "empty", "oversized"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "image.tar")
			var err error
			switch scenario {
			case "symlink":
				err = os.Symlink("/etc/passwd", file)
			case "fifo":
				err = syscall.Mkfifo(file, 0600)
			default:
				err = os.WriteFile(file, nil, 0600)
				if err == nil && scenario == "oversized" {
					err = os.Truncate(file, api.MaxExportedLayerBytes+1)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := buildIssueSourceMapImageSHA256(dir); err == nil {
				t.Fatal("unsafe image accepted")
			}
		})
	}
}
