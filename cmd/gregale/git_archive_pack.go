package main

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/secretscan"
)

const (
	maxGitArchiveEntries = 10_000
	maxGitignoreBytes    = 1 << 20
)

// packGitArchive filters the committed archive produced by git archive. Git
// gives us a faithful snapshot of HEAD, but it does not apply the deploy
// packer's build-artifact exclusions, .gregaleignore, or secret scan. This
// pass keeps the HEAD-only guarantee while bringing those policies to the
// Git-origin zero-config path.
//
// The archive is streamed entry-by-entry. Only files small enough for the
// source-tree scanner are held in memory; all other regular files are copied
// directly through the tar writer.
func packGitArchive(srcPath string, capMB int, mode secretScanMode) (outPath string, regularFileCount int, findings []secretscan.Finding, err error) {
	if capMB <= 0 {
		return "", 0, nil, fmt.Errorf("invalid source cap %d MB", capMB)
	}
	patterns, err := readGitArchiveIgnore(srcPath)
	if err != nil {
		return "", 0, nil, fmt.Errorf("read .gregaleignore: %w", err)
	}

	in, err := openCustomerFile(srcPath)
	if err != nil {
		return "", 0, nil, fmt.Errorf("open git archive: %w", err)
	}
	defer func() { _ = in.Close() }()
	gzIn, err := gzip.NewReader(in)
	if err != nil {
		return "", 0, nil, fmt.Errorf("open git archive gzip: %w", err)
	}
	defer func() { _ = gzIn.Close() }()

	out, err := os.CreateTemp("", "gregale-filtered-*.tar.gz")
	if err != nil {
		return "", 0, nil, fmt.Errorf("create filtered archive: %w", err)
	}
	outPath = out.Name()
	success := false
	defer func() {
		if !success {
			_ = out.Close()
			_ = os.Remove(outPath)
			outPath = ""
		}
	}()

	gzOut := gzip.NewWriter(out)
	tw := tar.NewWriter(gzOut)
	defer func() {
		_ = tw.Close()
		_ = gzOut.Close()
		_ = out.Close()
	}()

	capBytes := int64(capMB) * 1024 * 1024
	var totalUncompressed int64
	entries := 0
	tr := tar.NewReader(gzIn)
	for {
		hdr, nextErr := tr.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return "", 0, findings, fmt.Errorf("read git archive: %w", nextErr)
		}
		entries++
		if entries > maxGitArchiveEntries {
			return "", 0, findings, fmt.Errorf("git archive has more than %d entries", maxGitArchiveEntries)
		}
		if !gitArchiveEntrySafe(hdr.Name) {
			return "", 0, findings, fmt.Errorf("unsafe git archive entry %q", hdr.Name)
		}
		if hdr.Typeflag == tar.TypeXGlobalHeader || hdr.Typeflag == tar.TypeXHeader {
			// git archive emits a PAX global-header record before
			// the actual tree. tar.Reader exposes it as metadata; it
			// is not a deployable archive entry.
			continue
		}

		isDir := hdr.Typeflag == tar.TypeDir
		name := strings.TrimSuffix(hdr.Name, "/")
		if name == "" {
			continue
		}
		if gitArchiveShouldExclude(name, isDir, patterns) {
			continue
		}

		inputSize := hdr.Size
		var (
			body     []byte
			scanThis bool
		)
		switch hdr.Typeflag {
		case tar.TypeDir:
			hdr.Name = name + "/"
			hdr.Size = 0
		case tar.TypeReg:
			if inputSize < 0 || inputSize > capBytes {
				return "", 0, findings, fmt.Errorf("git archive entry %q exceeds the %d MB per-file cap", name, capMB)
			}
			if inputSize > capBytes-totalUncompressed {
				return "", 0, findings, fmt.Errorf("uncompressed git archive is over the %d MB zero-config cap", capMB)
			}
			hdr.Name = name
			body, scanThis = readGitArchiveEntryForScan(name, inputSize, mode)
			if scanThis {
				n, readErr := io.ReadFull(tr, body)
				if readErr != nil {
					return "", 0, findings, fmt.Errorf("read git archive entry %q: %w", name, readErr)
				}
				if int64(n) != inputSize {
					return "", 0, findings, fmt.Errorf("short git archive entry %q", name)
				}
				var entryFindings []secretscan.Finding
				body, entryFindings = scanGitArchiveEntry(name, body, mode)
				findings = append(findings, entryFindings...)
				hdr.Size = int64(len(body))
				if hdr.Size > capBytes || hdr.Size > capBytes-totalUncompressed {
					return "", 0, findings, fmt.Errorf("redacted git archive entry %q exceeds the %d MB zero-config cap", name, capMB)
				}
			}
		default:
			return "", 0, findings, fmt.Errorf("git archive entry %q has unsupported type %d", name, hdr.Typeflag)
		}

		hdr.ModTime = packEpoch
		hdr.AccessTime = time.Time{}
		hdr.ChangeTime = time.Time{}
		hdr.PAXRecords = nil
		if err := tw.WriteHeader(hdr); err != nil {
			return "", 0, findings, fmt.Errorf("write git archive header %q: %w", name, err)
		}
		if isDir {
			continue
		}

		if scanThis {
			if n, writeErr := tw.Write(body); writeErr != nil {
				return "", 0, findings, fmt.Errorf("write git archive entry %q: %w", name, writeErr)
			} else if n != len(body) {
				return "", 0, findings, fmt.Errorf("short write for git archive entry %q: wrote %d of %d bytes", name, n, len(body))
			}
		} else if _, copyErr := io.CopyN(tw, tr, hdr.Size); copyErr != nil {
			return "", 0, findings, fmt.Errorf("copy git archive entry %q: %w", name, copyErr)
		}
		totalUncompressed += hdr.Size
		regularFileCount++
	}
	if mode.isStrict() && len(findings) > 0 {
		return "", 0, findings, &StrictSecretScanError{
			Findings: findings,
			Hint:     "move detected secrets to gregale secrets set (see " + cliDocsURL + ")",
		}
	}
	if err := tw.Close(); err != nil {
		return "", 0, findings, fmt.Errorf("close filtered tar: %w", err)
	}
	if err := gzOut.Close(); err != nil {
		return "", 0, findings, fmt.Errorf("close filtered gzip: %w", err)
	}
	if err := out.Close(); err != nil {
		return "", 0, findings, fmt.Errorf("close filtered archive: %w", err)
	}
	st, err := os.Stat(outPath)
	if err != nil {
		return "", 0, findings, fmt.Errorf("stat filtered archive: %w", err)
	}
	if st.Size() > capBytes {
		return "", 0, findings, fmt.Errorf("filtered git archive is %d MB, over the %d MB zero-config cap", st.Size()/(1024*1024), capMB)
	}
	success = true
	return outPath, regularFileCount, findings, nil
}

// readGitArchiveIgnore reads the root .gregaleignore entry before the second
// archive pass. A bounded read prevents a tracked configuration file from
// turning the zero-config pack into an unbounded allocation.
func readGitArchiveIgnore(srcPath string) ([]gregaleignorePattern, error) {
	f, err := openCustomerFile(srcPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if strings.TrimSuffix(hdr.Name, "/") != gregaleignoreFile {
			continue
		}
		if hdr.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("%s is not a regular file", gregaleignoreFile)
		}
		data, err := io.ReadAll(io.LimitReader(tr, maxGitignoreBytes+1))
		if err != nil {
			return nil, err
		}
		if len(data) > maxGitignoreBytes {
			return nil, fmt.Errorf("%s exceeds %d bytes", gregaleignoreFile, maxGitignoreBytes)
		}
		return parseGregaleignore(data), nil
	}
}

func gitArchiveEntrySafe(name string) bool {
	// Directory headers conventionally carry one trailing slash. Strip
	// exactly that slash before checking components, while still rejecting
	// empty interior components such as "a//b".
	name = strings.TrimSuffix(name, "/")
	if name == "" || strings.Contains(name, "\\") || strings.ContainsRune(name, 0) || strings.HasPrefix(name, "/") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

// gitArchiveShouldExclude mirrors filepath.WalkDir's SkipDir behaviour for
// archives that omit explicit directory headers (git archive commonly does).
// Checking only the file basename would otherwise let a tracked file below
// node_modules/.git/etc. survive the filter.
func gitArchiveShouldExclude(name string, isDir bool, patterns []gregaleignorePattern) bool {
	if shouldExclude(name, isDir, patterns) {
		return true
	}
	trimmed := strings.TrimSuffix(name, "/")
	parts := strings.Split(trimmed, "/")
	for i := 1; i < len(parts); i++ {
		if shouldExclude(strings.Join(parts[:i], "/"), true, patterns) {
			return true
		}
	}
	return false
}

func readGitArchiveEntryForScan(name string, size int64, mode secretScanMode) ([]byte, bool) {
	if !mode.isScanEnabled() || size > treeScanMaxBytes {
		return nil, false
	}
	if isEnvFileBase(name) || mode.isSourceTree() {
		return make([]byte, size), true
	}
	return nil, false
}

func scanGitArchiveEntry(name string, data []byte, mode secretScanMode) ([]byte, []secretscan.Finding) {
	if isEnvFileBase(name) {
		findings := secretscan.ScanEnvContent(name, data)
		if len(findings) > 0 {
			return redactEnvContent(data, findings), findings
		}
		return data, nil
	}
	if mode.isSourceTree() && secretscan.IsTextFile(name, data) {
		findings := secretscan.ScanFile(name, data)
		if len(findings) > 0 {
			return redactFileContent(data, findings), findings
		}
	}
	return data, nil
}
