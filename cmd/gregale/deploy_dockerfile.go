package main

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"strings"

	faastarball "github.com/onebox-faas/faas/pkg/tarball"
)

// validateExplicitDockerfileArchive keeps the local preview aligned with the
// multipart apply contract. It only checks the selected source root and never
// executes or extracts customer content.
func validateExplicitDockerfileArchive(path, sourceRoot string) error {
	logicalRoot, err := faastarball.ResolveSourceRoot(path, sourceRoot)
	if err != nil {
		return fmt.Errorf("inspect source archive: %w", err)
	}
	f, err := openCustomerFile(path)
	if err != nil {
		return fmt.Errorf("open source archive: %w", err)
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("read source archive: %w", err)
	}
	defer func() { _ = zr.Close() }()

	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("no Dockerfile found at selected source root %q", displaySourceRoot(sourceRoot))
		}
		if err != nil {
			return fmt.Errorf("read source archive: %w", err)
		}
		name := strings.TrimPrefix(strings.TrimSuffix(hdr.Name, "/"), "./")
		want := "Dockerfile"
		if logicalRoot != "" {
			want = strings.TrimSuffix(logicalRoot, "/") + "/Dockerfile"
		}
		if name == want && hdr.Typeflag != tar.TypeDir {
			return nil
		}
	}
}

func displaySourceRoot(root string) string {
	root = strings.TrimSpace(root)
	if root == "" || root == "." {
		return "."
	}
	return root
}
