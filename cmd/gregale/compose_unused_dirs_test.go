package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func writeComposeTree(t *testing.T, root, compose string, dirs ...string) {
	t.Helper()
	writeTargetFile(t, filepath.Join(root, "compose.yaml"), compose)
	for _, dir := range dirs {
		writeTargetFile(t, filepath.Join(root, dir, "README"), "x\n")
	}
}

func TestComposeUnusedTopLevelDirs(t *testing.T) {
	for _, tc := range []struct {
		name, compose string
		want          []string
	}{
		{name: "build contexts, env files, secrets and includes are used",
			compose: "include: [extra/compose.yaml]\nservices:\n  web:\n    build: ./src/web\n    env_file: [config/web.env]\n  cart:\n    build: {context: ./src/cart, dockerfile: ../../docker/cart.Dockerfile}\nsecrets:\n  key: {file: ./keys/key.pem}\n",
			want:    []string{"docs", "helm-chart", "kubernetes-manifests"}},
		{name: "a root build context uses everything",
			compose: "services:\n  web:\n    build: .\n", want: nil},
		{name: "an interpolated path keeps everything",
			compose: "services:\n  web:\n    build: ${WEB_DIR}\n", want: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeComposeTree(t, root, tc.compose, "src/web", "src/cart", "config", "docker", "keys", "extra", "docs", "helm-chart", "kubernetes-manifests", ".github")
			if got := composeUnusedTopLevelDirs(root); !slices.Equal(got, tc.want) {
				t.Fatalf("unused = %v, want %v", got, tc.want)
			}
		})
	}
	if got := composeUnusedTopLevelDirs(t.TempDir()); got != nil {
		t.Fatalf("no compose file, unused = %v", got)
	}
}

// microservices-demo shape: the services fit the entry limit, the deployment
// manifests beside them do not. The pack leaves them out instead of failing.
func TestPackLeavesOutComposeUnusedDirsOnlyOverTheEntryLimit(t *testing.T) {
	root := t.TempDir()
	writeComposeTree(t, root, "services:\n  web:\n    build: ./src/web\n    ports: [\"8080\"]\n", "src/web")
	writeTargetFile(t, filepath.Join(root, "src", "web", "Dockerfile"), "FROM scratch\n")
	manifests := filepath.Join(root, "kubernetes-manifests")
	if err := os.MkdirAll(manifests, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= api.SourceArchiveMaxEntries; i++ {
		if err := os.WriteFile(filepath.Join(manifests, fmt.Sprintf("m%05d.yaml", i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	archive := filepath.Join(t.TempDir(), "src.tar.gz")
	if _, err := packDirToTarGzReported(root, archive, 100, nil, "", nil, false, nil); err != nil {
		t.Fatalf("pack over the limit failed: %v", err)
	}
	names := tarNames(t, archive)
	if !slices.Contains(names, "src/web/Dockerfile") || !slices.Contains(names, "compose.yaml") {
		t.Fatalf("service source missing: %v", names)
	}
	for _, name := range names {
		if strings.HasPrefix(name, "kubernetes-manifests") {
			t.Fatalf("unused directory packed: %s", name)
		}
	}
}

func tarNames(t *testing.T, archive string) []string {
	t.Helper()
	f, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var names []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return names
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, strings.TrimSuffix(hdr.Name, "/"))
	}
}
