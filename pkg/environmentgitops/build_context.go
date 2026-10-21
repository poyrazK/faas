package environmentgitops

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sourcecontext"
)

const BuildContextFormat = api.EnvironmentGitBuildContextFormat

type contextEntry struct {
	header tar.Header
	offset int64
}

// CanonicalBuildContext delivers exactly the files whose bytes determine the
// archive digest. It keeps repository-relative paths, strips only the provider
// wrapper, sorts entries, and normalizes transport timestamps and ownership.
// Files outside context are never delivered to the builder. No source path is
// extracted on the host; the bounded expanded tar is a temporary opaque spool.
// The first version accepts regular files/directories only, so links cannot
// escape the declared context or change meaning when its wrapper is removed.
func CanonicalBuildContext(reader io.Reader, writer io.Writer, context string, maxBytes int64) error {
	context, err := sourcecontext.Normalize(context)
	if err != nil || maxBytes <= 0 {
		return fmt.Errorf("invalid Git build context")
	}
	compressed := &io.LimitedReader{R: reader, N: maxBytes + 1}
	gz, err := gzip.NewReader(compressed)
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	spool, err := os.CreateTemp("", "gregale-git-context-*.tar")
	if err != nil {
		return err
	}
	defer func() { _ = spool.Close(); _ = os.Remove(spool.Name()) }()
	n, err := io.Copy(spool, io.LimitReader(gz, api.EnvironmentGitOpsMaxExpandedArchiveBytes+1))
	if err != nil || n > api.EnvironmentGitOpsMaxExpandedArchiveBytes || compressed.N <= 0 {
		return fmt.Errorf("git build context exceeds limits or is truncated")
	}
	counted := &contextCounter{Reader: io.NewSectionReader(spool, 0, n)}
	archive := tar.NewReader(counted)
	entries := []contextEntry{}
	seen, root := map[string]bool{}, ""
	files := 0
	for count := 0; ; count++ {
		h, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || count >= api.SourceArchiveMaxEntries {
			return fmt.Errorf("invalid or oversized Git build context")
		}
		name := strings.TrimSuffix(h.Name, "/")
		if !fs.ValidPath(name) || strings.ContainsAny(name, "\\\x00") || path.Clean(name) != name || name == "." {
			return fmt.Errorf("unsafe Git build context path")
		}
		parts := strings.SplitN(name, "/", 2)
		if root == "" {
			root = parts[0]
		}
		if parts[0] != root || seen[name] {
			return fmt.Errorf("ambiguous Git build context")
		}
		seen[name] = true
		if len(parts) == 1 {
			if h.Typeflag != tar.TypeDir {
				return fmt.Errorf("git build context requires a directory wrapper")
			}
			continue
		}
		rel := parts[1]
		if context != "." && rel != context && !strings.HasPrefix(rel, context+"/") {
			continue
		}
		for key := range h.PAXRecords {
			if strings.HasPrefix(key, "GNU.sparse.") {
				return fmt.Errorf("sparse Git build context entries are unsupported")
			}
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir {
			return fmt.Errorf("unchanged build contexts support only regular files and directories")
		}
		if h.Typeflag == tar.TypeReg {
			files++
		}
		// A transport prefix prevents the single-root extraction heuristic from
		// stripping a real first-level directory. It is disambiguated below.
		header := tar.Header{Name: "gregale-source/" + rel, Typeflag: h.Typeflag, Mode: h.Mode & 0o777, Size: h.Size, Format: tar.FormatPAX}
		if h.Typeflag == tar.TypeDir {
			header.Size = 0
		}
		entries = append(entries, contextEntry{header: header, offset: counted.n})
	}
	if _, err := io.Copy(gitArchivePadding{}, counted); err != nil || files == 0 {
		return fmt.Errorf("empty or malformed Git build context")
	}
	// ResolveSourceRoot gives a real repository-relative root precedence over
	// transport wrapping. Choose a wrapper that cannot also be a real first
	// component, including when the customer has a gregale-source directory.
	topNames := map[string]bool{}
	for _, entry := range entries {
		rel := strings.TrimPrefix(entry.header.Name, "gregale-source/")
		topNames[strings.SplitN(rel, "/", 2)[0]] = true
	}
	wrapper := "gregale-source"
	for suffix := 1; topNames[wrapper]; suffix++ {
		wrapper = fmt.Sprintf("gregale-source-%d", suffix)
	}
	for i := range entries {
		entries[i].header.Name = wrapper + "/" + strings.TrimPrefix(entries[i].header.Name, "gregale-source/")
	}
	slices.SortFunc(entries, func(a, b contextEntry) int { return strings.Compare(a.header.Name, b.header.Name) })
	bounded := &contextWriter{writer: writer, remaining: maxBytes}
	out := gzip.NewWriter(bounded)
	tw := tar.NewWriter(out)
	for _, entry := range entries {
		if err := tw.WriteHeader(&entry.header); err != nil {
			return err
		}
		if _, err := io.Copy(tw, io.NewSectionReader(spool, entry.offset, entry.header.Size)); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return out.Close()
}

type contextCounter struct {
	io.Reader
	n int64
}

func (c *contextCounter) Read(p []byte) (int, error) {
	n, err := c.Reader.Read(p)
	c.n += int64(n)
	return n, err
}

type contextWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *contextWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, fmt.Errorf("canonical Git build context exceeds archive limit")
	}
	n, err := w.writer.Write(p)
	w.remaining -= int64(n)
	return n, err
}
