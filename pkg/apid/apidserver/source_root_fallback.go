package apidserver

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/markers"
	"github.com/onebox-faas/faas/pkg/tarball"
)

// resolveArchiveSourceRoot checks that root names a directory in the archive
// and returns the root the build uses. When the caller did not name a
// source_root, root is the app's configured directory (a project service's
// path in the repository). An archive of that directory's contents alone
// does not contain it, and a raw API deploy of one failed with 400
// source_invalid `source_root "ui" is not present` until the caller added
// source_root=. (2026-11-12 agent feedback; the CLI sends "." itself). Such an
// archive now builds from its own root when that root holds an app marker,
// so a monorepo archive with a mistyped root still fails instead of building
// the repository root. An explicit source_root is never replaced.
func resolveArchiveSourceRoot(archivePath, root string, explicit bool) (resolved string, ok bool, err error) {
	if root == "" {
		return "", true, nil
	}
	present, err := archiveHasSourceRoot(archivePath, root)
	if err != nil || present {
		return root, present, err
	}
	if explicit {
		return root, false, nil
	}
	deployable, err := archiveRootHasAppMarker(archivePath)
	if err != nil || !deployable {
		return root, false, err
	}
	return "", true, nil
}

// archiveRootHasAppMarker reports whether the archive's logical root holds a
// file that identifies a deployable app: a build marker such as Dockerfile,
// package.json, requirements.txt or go.mod, or a gregale.yaml.
func archiveRootHasAppMarker(archivePath string) (bool, error) {
	logicalRoot, err := archiveLogicalRoot(archivePath, "")
	if err != nil {
		return false, err
	}
	f, err := os.Open(archivePath) //nolint:forbidigo // server-owned spool file; see archiveHasSourceRoot.
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return false, err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		rel, ok := archiveEntryRelativeToRoot(tarball.TrimLeadingDotSlash(hdr.Name), logicalRoot)
		if !ok || strings.Contains(rel, "/") {
			continue
		}
		name := path.Base(rel)
		if markers.IsAppMarker(name) || name == "gregale.yaml" || name == "gregale.yml" {
			return true, nil
		}
	}
}

// sourceRootMissingProblem names the absent root and what to send instead.
func sourceRootMissingProblem(root string, explicit bool) *api.Problem {
	detail := fmt.Sprintf("source_root %q is not present in the source archive", root)
	if explicit {
		detail += "; send source_root=. when the archive already holds only that directory"
	} else {
		detail += fmt.Sprintf(" (the app's configured root) and the archive root has no Dockerfile, package.json, requirements.txt, pyproject.toml, go.mod or gregale.yaml; upload the repository, or send source_root=. for an archive of %q alone", root)
	}
	return api.ErrSourceInvalid(detail)
}
