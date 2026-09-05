// Package tarball contains the source-archive shape rules shared by marker
// detection, version inference, and rootfs assembly.
package tarball

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/sourcecontext"
)

// RootPrefix returns the single transport-wrapper directory to strip from a
// gzipped source archive. GitHub codeload archives contain a project
// directory and may begin with a pax_global_header metadata record; metadata
// and directory entries do not count as project roots. A flat archive, or an
// archive with multiple real top-level roots, returns an empty prefix.
//
// The helper intentionally examines entry names only. tar.Reader advances
// past an entry body when Next is called, so callers do not need to buffer the
// source archive in memory just to determine its shape.
//
//nolint:forbidigo // callers pass apid-spooled source archives; the path is an internal artifact, not a customer path opened directly by this package.
func RootPrefix(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open: %w", err)
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("gzip: %w", err)
	}
	defer func() { _ = zr.Close() }()

	tr := tar.NewReader(zr)
	var prefix string
	var nested bool
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read tar: %w", err)
		}
		switch hdr.Typeflag {
		case tar.TypeDir, tar.TypeXHeader, tar.TypeXGlobalHeader,
			tar.TypeGNULongName, tar.TypeGNULongLink:
			continue
		}
		name := strings.TrimPrefix(strings.TrimSuffix(hdr.Name, "/"), "./")
		if name == "" {
			continue
		}
		first := name
		if i := strings.IndexByte(name, '/'); i >= 0 {
			first = name[:i]
			nested = true
		}
		if prefix == "" {
			prefix = first
		} else if prefix != first {
			return "", nil
		}
	}
	if nested {
		return prefix, nil
	}
	return "", nil
}

// ResolveSourceRoot maps a source_root value to the path used inside the
// archive. Empty and "." retain the legacy root-prefix behavior. For a
// nested source root, a direct repository-relative path wins over the
// historical single-directory wrapper heuristic; this matters for a minimal
// flat archive that contains only entries such as apps/api/package.json.
// When the direct path is absent, a detected transport wrapper is prepended.
func ResolveSourceRoot(path, sourceRoot string) (string, error) {
	root, err := sourcecontext.EffectiveRoot(sourceRoot)
	if err != nil {
		return "", err
	}
	prefix, err := RootPrefix(path)
	if err != nil {
		return "", err
	}
	if root == sourcecontext.DefaultRoot || prefix == "" {
		if root == sourcecontext.DefaultRoot {
			return prefix, nil
		}
		return root, nil
	}
	direct, err := archiveContainsRoot(path, root)
	if err != nil {
		return "", err
	}
	if direct {
		return root, nil
	}
	return prefix + "/" + root, nil
}

// archiveContainsRoot reports whether an archive contains an entry below
// root. Directory headers count too; callers that need a buildable source
// root apply their own marker/existence checks after this shape check.
func archiveContainsRoot(path, root string) (bool, error) {
	//nolint:forbidigo // callers pass apid-spooled source archives; see RootPrefix.
	f, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("open: %w", err)
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return false, fmt.Errorf("gzip: %w", err)
	}
	defer func() { _ = zr.Close() }()

	tr := tar.NewReader(zr)
	root = strings.TrimSuffix(strings.TrimPrefix(root, "./"), "/")
	prefix := root + "/"
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("read tar: %w", err)
		}
		switch hdr.Typeflag {
		case tar.TypeXHeader, tar.TypeXGlobalHeader,
			tar.TypeGNULongName, tar.TypeGNULongLink:
			continue
		}
		name := strings.TrimPrefix(strings.TrimSuffix(hdr.Name, "/"), "./")
		if name == root || strings.HasPrefix(name, prefix) {
			return true, nil
		}
	}
}
