package markers

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/sourcecontext"
	"github.com/onebox-faas/faas/pkg/tarball"
)

// DetectFromFS inspects the top-level entries of fsys (no
// recursion) and returns the framework. fsys is typically
// os.DirFS(srcDir) on the CLI side. Returns
// (FrameworkUnknown, nil) when no marker is found at the root —
// the caller decides whether that's an error.
//
// Nested entries (anything containing "/") are ignored, matching
// the server-side rule at the prior pkg/builderd/detect.go:67-72.
// The CLI uses the same shape so a project root marker is the
// same on both sides.
//
// The marker list is iterated in priority order (Dockerfile
// first), so DetectFromFS and DetectFromTarball return identical
// answers on identical inputs — this is the parity contract
// pinned by TestDetectCLIParity.
func DetectFromFS(fsys fs.FS) (Framework, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return FrameworkUnknown, fmt.Errorf("markers: read fs: %w", err)
	}
	// Build a set of present top-level names (case-folded). Doing
	// the lookup via a set rather than per-entry is O(N+M) instead
	// of O(N*M), which matters when the marker list grows.
	present := make(map[string]bool, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		// fs.DirEntry names from fs.ReadDir(fsys, ".") are base
		// names by contract, so a "/" check is unnecessary here.
		present[strings.ToLower(e.Name())] = true
	}
	for _, m := range appMarkers {
		if present[strings.ToLower(m.filename)] {
			return m.framework, nil
		}
	}
	return FrameworkUnknown, nil
}

// DetectFromTarball opens the gzipped tarball at path and
// returns the framework. Symmetric with DetectFromFS — both
// return the same answer on the same input (parity pinned by
// TestDetectCLIParity). Both return (FrameworkUnknown, nil)
// when no marker is found at the root; a non-nil error is
// reserved for IO failures (open, gzip, tar read).
//
// gregale pack archives one directory level (for example
// app/package.json) because it preserves the packed source
// directory's basename. Such an archive is treated as a project
// root only when all regular entries share one common top-level
// prefix and there are no regular files at the archive root. This
// keeps an ordinary nested package.json from changing detection.
//
//nolint:forbidigo // path is the apid-spooled tarball that already passed apid's validateTarballShape (in cmd/apid/deploy_inputs.go) before builderd received the build notification. Symlink-attack impossible because apid wrote the file with a fresh random id. Direct unit-test callers construct the path themselves; rationale holds. The original comment lived at pkg/builderd/detect.go:40 and applies unchanged here.
func DetectFromTarball(path string) (Framework, error) {
	return DetectFromTarballAtRoot(path, sourcecontext.DefaultRoot)
}

// DetectFromTarballAtRoot inspects only the immediate files below sourceRoot
// in a source archive. sourceRoot is relative to the logical project root;
// the legacy single-directory wrapper used by the CLI is recognized and
// ignored for this purpose. This lets a repository-context archive expose
// root-level workspace files without making a sibling package's package.json
// decide the selected service's framework.
func DetectFromTarballAtRoot(path, sourceRoot string) (Framework, error) {
	root, err := sourcecontext.Normalize(sourceRoot)
	if err != nil {
		return FrameworkUnknown, fmt.Errorf("markers: invalid source root: %w", err)
	}
	if root != sourcecontext.DefaultRoot {
		return detectFromTarballNestedRoot(path, root)
	}

	f, err := os.Open(path) //nolint:forbidigo // path is the apid-spooled tarball that passed validateTarballShape before builderd receives it; the spool file is server-owned.
	if err != nil {
		return FrameworkUnknown, fmt.Errorf("markers: open: %w", err)
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return FrameworkUnknown, fmt.Errorf("markers: gzip: %w", err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	present := map[string]bool{}
	prefixed := map[string]map[string]bool{}
	prefixes := map[string]bool{}
	hasRootFile := false
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return FrameworkUnknown, fmt.Errorf("markers: read tar: %w", err)
		}
		// Skip directory and archive-metadata entries. GitHub's
		// codeload tarballs begin with a pax_global_header entry;
		// treating that metadata record as a root file makes a
		// perfectly valid wrapped project (for example
		// cron-shot-<sha>/package.json) look like it has mixed
		// roots, so the real marker is never promoted into `present`.
		// Directory entries are also skipped for parity with
		// DetectFromFS, which drops IsDir() entries.
		switch hdr.Typeflag {
		case tar.TypeDir, tar.TypeXHeader, tar.TypeXGlobalHeader,
			tar.TypeGNULongName, tar.TypeGNULongLink:
			continue
		}
		name := strings.TrimPrefix(hdr.Name, "./")
		parts := strings.Split(name, "/")
		if len(parts) == 1 {
			hasRootFile = true
			present[strings.ToLower(parts[0])] = true
			continue
		}
		prefixes[parts[0]] = true
		if len(parts) == 2 && parts[0] != "" {
			if prefixed[parts[0]] == nil {
				prefixed[parts[0]] = map[string]bool{}
			}
			prefixed[parts[0]][strings.ToLower(parts[1])] = true
		}
	}
	if !hasRootFile && len(prefixes) == 1 {
		for _, files := range prefixed {
			for name := range files {
				present[name] = true
			}
		}
	}
	for _, m := range appMarkers {
		if present[strings.ToLower(m.filename)] {
			return m.framework, nil
		}
	}
	// No marker at root — return (FrameworkUnknown, nil) so
	// the parity contract holds: both sides answer the same
	// way for unknown input. The CLI's pre-refactor
	// detectFramework similarly returned fwUnknown without an
	// error; the server's prior pkg/builderd.detch.Detect
	// returned an error, but the parity test pins the
	// CLI shape as authoritative. Callers that need an error
	// can wrap the (unknown, nil) tuple.
	return FrameworkUnknown, nil
}

func detectFromTarballNestedRoot(path, sourceRoot string) (Framework, error) {
	logicalRoot, err := tarball.ResolveSourceRoot(path, sourceRoot)
	if err != nil {
		return FrameworkUnknown, fmt.Errorf("markers: source root: %w", err)
	}

	f, err := os.Open(path) //nolint:forbidigo // path is the apid-spooled tarball that passed validateTarballShape before builderd receives it; the spool file is server-owned.
	if err != nil {
		return FrameworkUnknown, fmt.Errorf("markers: open: %w", err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return FrameworkUnknown, fmt.Errorf("markers: gzip: %w", err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	present := map[string]bool{}
	prefixWithSlash := strings.TrimSuffix(logicalRoot, "/") + "/"
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return FrameworkUnknown, fmt.Errorf("markers: read tar: %w", err)
		}
		switch hdr.Typeflag {
		case tar.TypeDir, tar.TypeXHeader, tar.TypeXGlobalHeader,
			tar.TypeGNULongName, tar.TypeGNULongLink:
			continue
		}
		name := strings.TrimPrefix(hdr.Name, "./")
		if !strings.HasPrefix(name, prefixWithSlash) {
			continue
		}
		rel := strings.TrimPrefix(name, prefixWithSlash)
		if rel == "" || strings.Contains(rel, "/") {
			continue
		}
		present[strings.ToLower(rel)] = true
	}
	for _, m := range appMarkers {
		if present[strings.ToLower(m.filename)] {
			return m.framework, nil
		}
	}
	return FrameworkUnknown, nil
}
