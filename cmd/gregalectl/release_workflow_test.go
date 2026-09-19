package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseBuildsCLITargetsInParallelBeforePublishing(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	workflow := string(body)

	for _, target := range []string{
		"- goos: linux\n            goarch: amd64",
		"- goos: linux\n            goarch: arm64",
		"- goos: darwin\n            goarch: amd64",
		"- goos: darwin\n            goarch: arm64",
	} {
		if !strings.Contains(workflow, target) {
			t.Errorf("release CLI matrix is missing %q", target)
		}
	}

	matrix := strings.Index(workflow, "  cli-targets:")
	fanIn := strings.Index(workflow, "    needs: [cli-targets]")
	download := strings.Index(workflow, "pattern: gregale-cli-target-*-${{ github.sha }}")
	restoreMode := strings.Index(workflow, `chmod 0755 "$binary"`)
	checksums := strings.Index(workflow, "sha256sum gregale gregale-darwin-arm64 gregale_*.tar.gz > CLI-SHA256SUMS")
	release := strings.Index(workflow, "name: Create GitHub Release")
	if matrix < 0 || fanIn < 0 || download < 0 || restoreMode < 0 || checksums < 0 || release < 0 {
		t.Fatalf("release workflow is missing matrix fan-in: matrix=%d fanIn=%d download=%d restoreMode=%d checksums=%d release=%d", matrix, fanIn, download, restoreMode, checksums, release)
	}
	if !(matrix < fanIn && fanIn < download && download < restoreMode && restoreMode < checksums && checksums < release) {
		t.Fatalf("release CLI fan-in is out of order: matrix=%d fanIn=%d download=%d restoreMode=%d checksums=%d release=%d", matrix, fanIn, download, restoreMode, checksums, release)
	}
	if strings.Contains(workflow, "for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64") {
		t.Fatal("release workflow still cross-builds all CLI targets serially")
	}
}
