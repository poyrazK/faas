package main

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// These limits are the largest source envelope accepted by any current plan.
// Keeping the CLI extraction envelope at the platform maximum avoids making a
// valid lower-plan archive fail before the server can return its plan-specific
// error, while still bounding local disk use from hostile compressed input.
const (
	deployArchiveMaxCompressedBytes int64 = 250 << 20
	deployArchiveMaxFileBytes       int64 = 256 << 20
	deployArchiveMaxExpandedBytes   int64 = deployArchiveMaxCompressedBytes * 5 / 2
)

// materializeDeployArchive snapshots and extracts a customer-supplied source
// archive into a private temporary directory. The returned archivePath is the
// snapshot that must be uploaded, and sourceDir is the corresponding extracted
// view used by all local deploy metadata scans. Keeping both under one cleanup
// root makes preview and apply observe exactly the bytes that are uploaded.
func materializeDeployArchive(input string) (archivePath, sourceDir string, cleanup func(), err error) {
	root, err := os.MkdirTemp("", "gregale-deploy-source-")
	if err != nil {
		return "", "", nil, fmt.Errorf("could not create deploy source directory: %w", err)
	}
	fail := func(err error) (string, string, func(), error) {
		_ = os.RemoveAll(root)
		return "", "", nil, err
	}

	name := filepath.Base(input)
	if name == "" || name == "." || name == ".." || name == string(filepath.Separator) {
		return fail(fmt.Errorf("invalid archive path %q", input))
	}
	archivePath = filepath.Join(root, name)
	in, err := openCustomerFile(input)
	if err != nil {
		return fail(err)
	}
	info, err := in.Stat()
	if err != nil {
		_ = in.Close()
		return fail(fmt.Errorf("could not stat archive: %w", err))
	}
	if info.Size() > deployArchiveMaxCompressedBytes {
		_ = in.Close()
		return fail(fmt.Errorf("archive is too large (%d bytes; cap is %d)", info.Size(), deployArchiveMaxCompressedBytes))
	}
	out, err := os.OpenFile(archivePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		_ = in.Close()
		return fail(fmt.Errorf("could not snapshot archive: %w", err))
	}
	copied, copyErr := io.Copy(out, io.LimitReader(in, deployArchiveMaxCompressedBytes+1))
	closeOutErr := out.Close()
	closeInErr := in.Close()
	if copyErr != nil {
		return fail(fmt.Errorf("could not snapshot archive: %w", copyErr))
	}
	if closeOutErr != nil {
		return fail(fmt.Errorf("could not close archive snapshot: %w", closeOutErr))
	}
	if closeInErr != nil {
		return fail(fmt.Errorf("could not close archive: %w", closeInErr))
	}
	if copied > deployArchiveMaxCompressedBytes {
		return fail(fmt.Errorf("archive is too large (%d bytes; cap is %d)", copied, deployArchiveMaxCompressedBytes))
	}

	extractDir := filepath.Join(root, "source")
	if err := os.Mkdir(extractDir, 0o700); err != nil {
		return fail(fmt.Errorf("could not create archive extraction directory: %w", err))
	}
	archiveRoot, err := extractDeployArchive(archivePath, extractDir)
	if err != nil {
		return fail(err)
	}
	cleanup = func() { _ = os.RemoveAll(root) }
	return archivePath, archiveRoot, cleanup, nil
}

func extractDeployArchive(archivePath, dst string) (string, error) {
	f, err := openCustomerFile(archivePath)
	if err != nil {
		return "", fmt.Errorf("could not open archive snapshot: %w", err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("archive is not a valid gzip stream: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)

	topLevel := map[string]struct{}{}
	nested := false
	seen := map[string]struct{}{}
	var entries, expanded int64
	for {
		// codeql[go/path-injection] false-positive: cleanDeployArchiveName
		// rejects absolute paths, volume names, NUL bytes, and every `..`
		// component before the value reaches filepath.Join below. The
		// deployArchivePathStaysUnder check is a second containment guard,
		// and dst is a fresh 0700 directory owned by this invocation.
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("invalid tar archive: %w", err)
		}
		// PAX/GNU metadata is consumed by archive/tar and never maps to a
		// filesystem object; accepting it is required for long filenames.
		switch hdr.Typeflag {
		case tar.TypeXHeader, tar.TypeXGlobalHeader, tar.TypeGNULongName, tar.TypeGNULongLink:
			if hdr.Size < 0 || hdr.Size > deployArchiveMaxFileBytes {
				return "", fmt.Errorf("archive metadata entry is too large (%d bytes; cap is %d)", hdr.Size, deployArchiveMaxFileBytes)
			}
			continue
		}
		if hdr.Name == "" {
			continue
		}
		name, err := cleanDeployArchiveName(hdr.Name)
		if err != nil {
			return "", err
		}
		if name == "" {
			continue
		}
		switch hdr.Typeflag {
		case tar.TypeReg, tar.TypeDir:
		default:
			return "", fmt.Errorf("archive entry %q has unsupported type %d (only files and directories are allowed)", hdr.Name, hdr.Typeflag)
		}
		entries++
		if entries > api.SourceArchiveMaxEntries {
			return "", fmt.Errorf("archive contains too many entries (>%d)", api.SourceArchiveMaxEntries)
		}
		if _, ok := seen[name]; ok {
			return "", fmt.Errorf("archive contains duplicate entry %q", name)
		}
		seen[name] = struct{}{}
		parts := strings.Split(name, "/")
		topLevel[parts[0]] = struct{}{}
		if len(parts) > 1 {
			nested = true
		}

		target := filepath.Join(dst, filepath.FromSlash(name))
		if !deployArchivePathStaysUnder(target, dst) {
			return "", fmt.Errorf("archive entry %q escapes extraction root", hdr.Name)
		}
		if hdr.Typeflag == tar.TypeDir {
			if hdr.Size != 0 {
				return "", fmt.Errorf("archive directory %q has a non-zero size", name)
			}
			if err := os.MkdirAll(target, 0o755); err != nil {
				return "", fmt.Errorf("could not create archive directory %q: %w", name, err)
			}
			continue
		}
		if hdr.Size < 0 || hdr.Size > deployArchiveMaxFileBytes {
			return "", fmt.Errorf("archive entry %q is too large (%d bytes; cap is %d)", name, hdr.Size, deployArchiveMaxFileBytes)
		}
		if expanded > deployArchiveMaxExpandedBytes-hdr.Size {
			return "", fmt.Errorf("expanded archive exceeds %d bytes", deployArchiveMaxExpandedBytes)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", fmt.Errorf("could not create parent directory for %q: %w", name, err)
		}
		mode := os.FileMode(0o644)
		if hdr.Mode&0o111 != 0 {
			mode = 0o755
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return "", fmt.Errorf("could not create archive file %q: %w", name, err)
		}
		written, copyErr := io.CopyN(out, tr, hdr.Size)
		closeErr := out.Close()
		if copyErr != nil {
			return "", fmt.Errorf("could not read archive file %q: %w", name, copyErr)
		}
		if closeErr != nil {
			return "", fmt.Errorf("could not close archive file %q: %w", name, closeErr)
		}
		if written != hdr.Size {
			return "", fmt.Errorf("archive file %q has unexpected size", name)
		}
		expanded += written
	}

	// Embedded templates and git archives carry one synthetic top-level
	// directory. Return that directory as the authoritative source view while
	// leaving flat archives rooted at the extraction directory.
	if nested && len(topLevel) == 1 {
		for prefix := range topLevel {
			return filepath.Join(dst, prefix), nil
		}
	}
	return dst, nil
}

func cleanDeployArchiveName(name string) (string, error) {
	if strings.ContainsRune(name, '\x00') {
		return "", fmt.Errorf("archive entry %q uses an absolute or invalid path", name)
	}
	name = strings.ReplaceAll(name, "\\", "/")
	localName := filepath.FromSlash(name)
	if filepath.IsAbs(localName) || filepath.VolumeName(localName) != "" {
		return "", fmt.Errorf("archive entry %q uses an absolute or invalid path", name)
	}
	for strings.HasPrefix(name, "./") {
		name = strings.TrimPrefix(name, "./")
	}
	name = strings.TrimSuffix(name, "/")
	if name == "" {
		return "", nil
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return "", fmt.Errorf("archive entry %q contains a parent-directory path", name)
		}
	}
	return name, nil
}

func deployArchivePathStaysUnder(target, root string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
