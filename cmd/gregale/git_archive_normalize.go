package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// `git archive` reproduces a commit faithfully, but three of its outputs used
// to make the default committed-HEAD deploy fail or ship the wrong bytes
// (ADR-20261026152731):
//
//   - a committed symlink became a symlink entry, which the packer and apid
//     refuse, so the deploy failed with "unsupported type 50";
//   - a submodule became an empty directory, silently;
//   - a Git LFS file became its pointer text, silently.
//
// normalizeGitArchive rewrites the archive into regular files and
// directories only: a symlink to a file or directory inside the archive is
// replaced by a copy of its target, an initialized submodule is archived at
// its recorded commit and merged in, and an LFS pointer is replaced by the
// object from the local LFS store. What cannot be resolved locally is
// reported as a warning naming the fix; nothing is dropped silently.

const (
	maxSymlinkHops      = 40
	maxSubmoduleDepth   = 8
	lfsPointerMaxBytes  = 1024
	lfsPointerSpecLine  = "version https://git-lfs.github.com/spec/v1"
	gitlinkMode         = "160000"
	gitArchiveTempGlob  = "gregale-git-normalized-*.tar.gz"
	gitArchivePAXGlobal = "pax_global_header"
)

// gitArchiveSource identifies the commit tree an archive was taken from.
type gitArchiveSource struct {
	repoRoot string // working tree of the repository (or submodule)
	prefix   string // tree path archived ("" = the whole commit)
	commit   string // "HEAD" or a submodule's recorded commit
}

type gitArchiveEntry struct {
	typ      byte
	linkname string
}

// normalizeGitArchive rewrites the gzipped `git archive` at archivePath in
// place and returns customer-facing warnings.
func normalizeGitArchive(src gitArchiveSource, archivePath string) ([]string, error) {
	return normalizeGitArchiveDepth(src, archivePath, 0)
}

func normalizeGitArchiveDepth(src gitArchiveSource, archivePath string, depth int) ([]string, error) {
	index, err := indexGitArchive(archivePath)
	if err != nil {
		return nil, err
	}
	var warnings []string
	fileLinks := map[string][]string{} // target file -> symlink names
	dirLinks := map[string][]string{}  // target dir prefix -> symlink names
	names := make([]string, 0, len(index))
	for name := range index {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if index[name].typ != tar.TypeSymlink {
			continue
		}
		target, typ, resolveErr := resolveArchiveLink(index, name, 0)
		switch {
		case resolveErr != nil:
			return nil, resolveErr
		case typ == tar.TypeReg:
			fileLinks[target] = append(fileLinks[target], name)
		case typ == tar.TypeDir:
			dirLinks[target] = append(dirLinks[target], name)
		default:
			warnings = append(warnings, fmt.Sprintf("symlink %s points to %s, which is not in the committed tree; it is not deployed", name, index[name].linkname))
		}
	}

	lfs := newLFSStore(src.repoRoot)
	out, err := os.CreateTemp(filepath.Dir(archivePath), gitArchiveTempGlob)
	if err != nil {
		return nil, fmt.Errorf("create normalized archive: %w", err)
	}
	outPath := out.Name()
	defer func() { _ = os.Remove(outPath) }()
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	w := &normalizedWriter{tw: tw, dirs: map[string]bool{}, lfs: lfs}

	err = forEachArchiveEntry(archivePath, func(hdr *tar.Header, body io.Reader) error {
		name := strings.TrimSuffix(hdr.Name, "/")
		switch hdr.Typeflag {
		case tar.TypeXGlobalHeader:
			return tw.WriteHeader(hdr)
		case tar.TypeDir:
			return w.dir(name, hdr)
		case tar.TypeReg:
			copies := append([]string(nil), fileLinks[name]...)
			for target, links := range dirLinks {
				if strings.HasPrefix(name, target+"/") {
					for _, link := range links {
						copies = append(copies, link+strings.TrimPrefix(name, target))
					}
				}
			}
			if len(copies) == 0 {
				return w.file(name, hdr, body)
			}
			return w.fileWithCopies(name, copies, hdr, body)
		case tar.TypeSymlink:
			return nil
		default:
			return fmt.Errorf("git archive entry %q has unsupported type %d", name, hdr.Typeflag)
		}
	})
	if err == nil {
		var subWarnings []string
		subWarnings, err = w.submodules(src, archivePath, depth)
		warnings = append(warnings, subWarnings...)
	}
	warnings = append(warnings, w.lfsMissing()...)
	if err == nil {
		err = tw.Close()
	}
	if err == nil {
		err = gz.Close()
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	if err := os.Rename(outPath, archivePath); err != nil {
		return nil, fmt.Errorf("publish normalized archive: %w", err)
	}
	return warnings, nil
}

func indexGitArchive(archivePath string) (map[string]gitArchiveEntry, error) {
	index := map[string]gitArchiveEntry{}
	err := forEachArchiveEntry(archivePath, func(hdr *tar.Header, _ io.Reader) error {
		if hdr.Typeflag == tar.TypeXGlobalHeader {
			return nil
		}
		index[strings.TrimSuffix(hdr.Name, "/")] = gitArchiveEntry{typ: hdr.Typeflag, linkname: hdr.Linkname}
		return nil
	})
	return index, err
}

func forEachArchiveEntry(archivePath string, fn func(*tar.Header, io.Reader) error) error {
	f, err := openCustomerFile(archivePath)
	if err != nil {
		return fmt.Errorf("open git archive: %w", err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("open git archive gzip: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read git archive: %w", err)
		}
		if !gitArchiveEntrySafe(hdr.Name) {
			return fmt.Errorf("unsafe git archive entry %q", hdr.Name)
		}
		if err := fn(hdr, tr); err != nil {
			return err
		}
	}
}

// resolveArchiveLink follows name (a symlink) through the archive, including
// symlinked parent directories, and returns the final path and its type. A
// zero type means the target is absent. Targets outside the archived tree
// are an error: their bytes are not part of this deploy.
func resolveArchiveLink(index map[string]gitArchiveEntry, name string, hops int) (string, byte, error) {
	if hops > maxSymlinkHops {
		return "", 0, fmt.Errorf("symlink %s: too many levels of symbolic links", name)
	}
	link := index[name].linkname
	if path.IsAbs(link) {
		return "", 0, fmt.Errorf("symlink %s points to the absolute path %s; replace it with the file or use a relative link inside the deployed directory", name, link)
	}
	target := path.Join(path.Dir(name), link)
	if target == ".." || strings.HasPrefix(target, "../") {
		return "", 0, fmt.Errorf("symlink %s points outside the deployed directory (%s); deploy the directory that contains its target or replace the link with a copy", name, link)
	}
	resolved := ""
	for _, part := range strings.Split(target, "/") {
		resolved = path.Join(resolved, part)
		entry, ok := index[resolved]
		if !ok {
			continue
		}
		if entry.typ == tar.TypeSymlink {
			next, typ, err := resolveArchiveLink(index, resolved, hops+1)
			if err != nil || typ == 0 {
				return next, typ, err
			}
			resolved = next
		}
	}
	entry, ok := index[resolved]
	if !ok {
		return resolved, 0, nil
	}
	return resolved, entry.typ, nil
}

type normalizedWriter struct {
	tw      *tar.Writer
	dirs    map[string]bool
	lfs     *lfsStore
	missing []string
}

func (w *normalizedWriter) dir(name string, hdr *tar.Header) error {
	if name == "" || w.dirs[name] {
		return nil
	}
	w.dirs[name] = true
	out := *hdr
	out.Name = name + "/"
	out.Typeflag = tar.TypeDir
	out.Size = 0
	return w.tw.WriteHeader(&out)
}

func (w *normalizedWriter) parents(name string, tmpl *tar.Header) error {
	dir := path.Dir(name)
	if dir == "." || w.dirs[dir] {
		return nil
	}
	if err := w.parents(dir, tmpl); err != nil {
		return err
	}
	return w.dir(dir, &tar.Header{Mode: 0o755, ModTime: tmpl.ModTime})
}

// file writes a regular entry, substituting a Git LFS pointer with its local
// object when one is present.
func (w *normalizedWriter) file(name string, hdr *tar.Header, body io.Reader) error {
	if err := w.parents(name, hdr); err != nil {
		return err
	}
	out := *hdr
	out.Name = name
	out.Typeflag = tar.TypeReg
	out.Linkname = ""
	if hdr.Size > lfsPointerMaxBytes {
		if err := w.tw.WriteHeader(&out); err != nil {
			return err
		}
		_, err := io.CopyN(w.tw, body, hdr.Size)
		return err
	}
	data, err := io.ReadAll(io.LimitReader(body, hdr.Size))
	if err != nil {
		return err
	}
	if oid, size, ok := parseLFSPointer(data); ok {
		object, objectErr := w.lfs.open(oid, size)
		if objectErr == nil {
			defer func() { _ = object.Close() }()
			out.Size = size
			if err := w.tw.WriteHeader(&out); err != nil {
				return err
			}
			_, err := io.CopyN(w.tw, object, size)
			return err
		}
		w.missing = append(w.missing, name)
	}
	out.Size = int64(len(data))
	if err := w.tw.WriteHeader(&out); err != nil {
		return err
	}
	_, err = w.tw.Write(data)
	return err
}

// fileWithCopies writes a symlink target once under its own name and again
// under every symlink that resolves to it, reading the body only once.
func (w *normalizedWriter) fileWithCopies(name string, copies []string, hdr *tar.Header, body io.Reader) error {
	spill, err := os.CreateTemp("", "gregale-git-link-*")
	if err != nil {
		return fmt.Errorf("buffer symlink target %s: %w", name, err)
	}
	defer func() { _ = spill.Close(); _ = os.Remove(spill.Name()) }()
	if _, err := io.CopyN(spill, body, hdr.Size); err != nil {
		return fmt.Errorf("buffer symlink target %s: %w", name, err)
	}
	for _, target := range append([]string{name}, copies...) {
		if _, err := spill.Seek(0, io.SeekStart); err != nil {
			return err
		}
		if err := w.file(target, hdr, spill); err != nil {
			return err
		}
	}
	return nil
}

// submodules merges every initialized submodule under src.prefix at the
// commit the tree records, and warns about the rest.
func (w *normalizedWriter) submodules(src gitArchiveSource, archivePath string, depth int) ([]string, error) {
	out, err := runGitCmd(src.repoRoot, "ls-tree", "-r", "-z", src.commit)
	if err != nil {
		return nil, fmt.Errorf("list submodules: %w", err)
	}
	var warnings []string
	for _, record := range strings.Split(out, "\x00") {
		meta, full, ok := strings.Cut(record, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 || fields[0] != gitlinkMode {
			continue
		}
		rel := full
		if src.prefix != "" {
			if !strings.HasPrefix(full, src.prefix+"/") {
				continue
			}
			rel = strings.TrimPrefix(full, src.prefix+"/")
		}
		sha := fields[2]
		if depth >= maxSubmoduleDepth {
			warnings = append(warnings, fmt.Sprintf("submodule %s is nested too deeply and is deployed empty", rel))
			continue
		}
		subRoot := filepath.Join(src.repoRoot, filepath.FromSlash(full))
		if _, statErr := os.Lstat(filepath.Join(subRoot, ".git")); statErr != nil {
			warnings = append(warnings, fmt.Sprintf("submodule %s is not checked out, so it is deployed empty; run `git submodule update --init --recursive`", rel))
			continue
		}
		if _, catErr := runGitCmd(subRoot, "cat-file", "-e", sha+"^{commit}"); catErr != nil {
			warnings = append(warnings, fmt.Sprintf("submodule %s does not have its recorded commit %s locally, so it is deployed empty; run `git submodule update --init --recursive`", rel, sha[:min(len(sha), 12)]))
			continue
		}
		subWarnings, mergeErr := w.mergeSubmodule(subRoot, sha, rel, archivePath, depth)
		if mergeErr != nil {
			return warnings, fmt.Errorf("archive submodule %s: %w", rel, mergeErr)
		}
		warnings = append(warnings, subWarnings...)
	}
	return warnings, nil
}

func (w *normalizedWriter) mergeSubmodule(subRoot, sha, rel, archivePath string, depth int) ([]string, error) {
	tmp, err := os.CreateTemp(filepath.Dir(archivePath), gitArchiveTempGlob)
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := runGitCmd(subRoot, "archive", sha, "--format=tar.gz", "-o", tmpPath); err != nil {
		return nil, err
	}
	warnings, err := normalizeGitArchiveDepth(gitArchiveSource{repoRoot: subRoot, commit: sha}, tmpPath, depth+1)
	if err != nil {
		return nil, err
	}
	for i := range warnings {
		warnings[i] = rel + ": " + warnings[i]
	}
	return warnings, forEachArchiveEntry(tmpPath, func(hdr *tar.Header, body io.Reader) error {
		name := strings.TrimSuffix(hdr.Name, "/")
		switch hdr.Typeflag {
		case tar.TypeDir:
			return w.dir(path.Join(rel, name), hdr)
		case tar.TypeReg:
			out := *hdr
			if err := w.parents(path.Join(rel, name), hdr); err != nil {
				return err
			}
			out.Name = path.Join(rel, name)
			if err := w.tw.WriteHeader(&out); err != nil {
				return err
			}
			_, err := io.CopyN(w.tw, body, hdr.Size)
			return err
		default:
			return nil
		}
	})
}

func (w *normalizedWriter) lfsMissing() []string {
	if len(w.missing) == 0 {
		return nil
	}
	shown := w.missing
	if len(shown) > 3 {
		shown = shown[:3]
	}
	more := ""
	if len(w.missing) > 3 {
		more = fmt.Sprintf(" and %d more", len(w.missing)-3)
	}
	return []string{fmt.Sprintf("Git LFS objects are not downloaded for %s%s, so their pointer files are deployed; run `git lfs pull`", strings.Join(shown, ", "), more)}
}

// parseLFSPointer recognises a Git LFS pointer file and returns its sha256
// object id and size.
func parseLFSPointer(data []byte) (string, int64, bool) {
	if !bytes.HasPrefix(data, []byte(lfsPointerSpecLine+"\n")) {
		return "", 0, false
	}
	var oid string
	var size int64 = -1
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		key, value, _ := strings.Cut(sc.Text(), " ")
		switch key {
		case "oid":
			oid = strings.TrimPrefix(value, "sha256:")
		case "size":
			if n, err := strconv.ParseInt(value, 10, 64); err == nil {
				size = n
			}
		}
	}
	if len(oid) != 64 || size < 0 || strings.Trim(oid, "0123456789abcdef") != "" {
		return "", 0, false
	}
	return oid, size, true
}

// lfsStore reads objects from a repository's local LFS store.
type lfsStore struct{ dir string }

func newLFSStore(repoRoot string) *lfsStore {
	common, err := runGitCmd(repoRoot, "rev-parse", "--git-common-dir")
	if err != nil {
		return &lfsStore{}
	}
	common = strings.TrimSpace(common)
	if !filepath.IsAbs(common) {
		common = filepath.Join(repoRoot, common)
	}
	return &lfsStore{dir: filepath.Join(common, "lfs", "objects")}
}

func (s *lfsStore) open(oid string, size int64) (*os.File, error) {
	if s.dir == "" {
		return nil, os.ErrNotExist
	}
	f, err := openCustomerFile(filepath.Join(s.dir, oid[:2], oid[2:4], oid))
	if err != nil {
		return nil, err
	}
	if info, statErr := f.Stat(); statErr != nil || info.Size() != size {
		_ = f.Close()
		return nil, os.ErrNotExist
	}
	return f, nil
}
