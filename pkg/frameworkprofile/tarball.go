package frameworkprofile

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/tarball"
)

// maxProfileArchiveBytes bounds the source bytes materialized for static
// profile inference. The deployment archive itself remains the source of
// truth; profile inference is advisory and must not turn a large upload into
// an unbounded apid allocation.
const maxProfileArchiveBytes = 32 << 20

// AnalyzeTarballAtRoot analyzes the same archive shape that builderd receives.
// sourceRoot is resolved with the shared transport-wrapper rules, so a
// repository archive and a selected monorepo member produce the same profile
// as an equivalent flat archive.
//
// The archive is materialized into a bounded fs.FS because the profile
// analyzer is intentionally independent of tar/gzip transport details. Files
// beyond the bound are skipped and the returned profile carries a warning; a
// truncated profile is never treated as an authoritative deployment failure.
func AnalyzeTarballAtRoot(archivePath, sourceRoot string) (Profile, error) {
	logicalRoot, err := tarball.ResolveSourceRoot(archivePath, sourceRoot)
	if err != nil {
		return Profile{}, fmt.Errorf("framework profile source root: %w", err)
	}
	files, truncated, err := readProfileFiles(archivePath, logicalRoot)
	if err != nil {
		return Profile{}, err
	}
	profile, err := Analyze(files)
	if err != nil {
		return Profile{}, err
	}
	if truncated {
		profile.Warnings = append(profile.Warnings, Warning{
			Code:    "profile_input_truncated",
			Message: "The source archive exceeded the static profile inspection budget; runtime verification remains authoritative.",
		})
	}
	return profile, nil
}

// readProfileFiles copies regular files below logicalRoot into a bounded
// in-memory filesystem. Individual files use the same 1 MiB cap as Analyze's
// source reads; skipping a large file is preferable to blocking deployment on
// a generated artifact that static inference cannot safely inspect.
//
//nolint:forbidigo // archivePath is the server-created, shape-validated spool file.
func readProfileFiles(archivePath, logicalRoot string) (fstest.MapFS, bool, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return nil, false, fmt.Errorf("framework profile open archive: %w", err)
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, false, fmt.Errorf("framework profile gzip archive: %w", err)
	}
	defer func() { _ = zr.Close() }()

	files := make(fstest.MapFS)
	tr := tar.NewReader(zr)
	var used int64
	truncated := false
	rootPrefix := strings.TrimSuffix(strings.TrimPrefix(logicalRoot, "./"), "/")
	if rootPrefix != "" {
		rootPrefix += "/"
	}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, false, fmt.Errorf("framework profile read archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := strings.TrimPrefix(hdr.Name, "./")
		if rootPrefix != "" {
			if !strings.HasPrefix(name, rootPrefix) {
				continue
			}
			name = strings.TrimPrefix(name, rootPrefix)
		}
		name = path.Clean(name)
		if name == "." || name == ".." || strings.HasPrefix(name, "../") {
			continue
		}
		if hdr.Size < 0 || hdr.Size > int64(maxSourceFileBytes) || used+hdr.Size > int64(maxProfileArchiveBytes) {
			truncated = true
			continue
		}
		body, err := io.ReadAll(io.LimitReader(tr, maxSourceFileBytes+1))
		if err != nil {
			return nil, false, fmt.Errorf("framework profile read %q: %w", name, err)
		}
		if len(body) > maxSourceFileBytes {
			truncated = true
			continue
		}
		files[name] = &fstest.MapFile{Data: body, Mode: fs.FileMode(0o644)}
		used += int64(len(body))
	}
	return files, truncated, nil
}
