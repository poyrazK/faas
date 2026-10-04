package routeimpact

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

type repository struct {
	root  string
	scope string
}

// limitedBuffer fails the subprocess rather than silently truncating evidence.
type limitedBuffer struct {
	bytes.Buffer
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.max-b.Len() {
		return 0, errors.New("route impact subprocess output exceeds its bound")
	}
	return b.Buffer.Write(p)
}

func runGit(ctx context.Context, dir string, input []byte, args ...string) ([]byte, error) {
	argv := append([]string{"--no-pager", "-C", dir}, args...)
	// Arguments are passed directly, never through a shell. Disable optional
	// index locks and replacement objects; inspection must not mutate the repo.
	cmd := exec.CommandContext(ctx, "git", argv...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_NO_REPLACE_OBJECTS=1")
	cmd.Stdin = bytes.NewReader(input)
	out := &limitedBuffer{max: api.RouteImpactGitOutputMaxBytes}
	cmd.Stdout = out
	cmd.Stderr = io.Discard // Git errors may quote refs or source; expose operation context only.
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s failed (check repository and revision): %w", args[0], err)
	}
	return out.Bytes(), nil
}

func openRepository(ctx context.Context, path string) (repository, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return repository{}, fmt.Errorf("resolve source directory: %w", err)
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return repository{}, fmt.Errorf("resolve source directory links: %w", err)
	}
	root, err := runGit(ctx, abs, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return repository{}, err
	}
	repoRoot, err := filepath.EvalSymlinks(strings.TrimSpace(string(root)))
	if err != nil {
		return repository{}, fmt.Errorf("resolve repository root: %w", err)
	}
	rel, err := filepath.Rel(repoRoot, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return repository{}, errors.New("source directory must be inside the repository")
	}
	scope := filepath.ToSlash(rel)
	if scope == "." {
		scope = ""
	}
	return repository{root: repoRoot, scope: scope}, nil
}

func (r repository) commit(ctx context.Context, ref string) (string, error) {
	body, err := runGit(ctx, r.root, nil, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(string(body))
	if len(sha) != 40 && len(sha) != 64 {
		return "", errors.New("git returned an invalid commit identity")
	}
	if _, err := hex.DecodeString(sha); err != nil {
		return "", fmt.Errorf("decode commit identity: %w", err)
	}
	return sha, nil
}

func (r repository) relative(path string) (string, bool) {
	if r.scope != "" {
		var found bool
		path, found = strings.CutPrefix(path, r.scope+"/")
		if !found {
			return "", false
		}
	}
	return path, path != "" && !strings.ContainsRune(path, '\x00')
}

func (r repository) tree(ctx context.Context, commit string) (sourceSnapshot, error) {
	body, err := runGit(ctx, r.root, nil, "ls-tree", "-r", "-z", "--full-tree", commit)
	if err != nil {
		return sourceSnapshot{}, err
	}
	snapshot := sourceSnapshot{meta: Snapshot{Revision: commit}, files: map[string]sourceFile{}}
	var objects []string
	var paths []string
	for _, row := range bytes.Split(body, []byte{0}) {
		if len(row) == 0 {
			continue
		}
		header, name, found := strings.Cut(string(row), "\t")
		fields := strings.Fields(header)
		if !found || len(fields) != 3 {
			return sourceSnapshot{}, errors.New("git returned an invalid tree entry")
		}
		path, scoped := r.relative(name)
		if !scoped {
			continue
		}
		snapshot.files[path] = sourceFile{path: path, hash: fields[2], mode: fields[0]}
		if len(snapshot.files) > api.RouteImpactMaxPaths {
			return sourceSnapshot{}, errors.New("source snapshot exceeds the route impact path limit")
		}
		if fields[1] != "blob" || fields[0] == "120000" {
			if strings.HasSuffix(path, ".py") || fields[1] == "commit" {
				snapshot.issues = append(snapshot.issues, Issue{Code: "unsupported_source_entry", File: path,
					Message: "Symlinked Python sources and submodules are not analyzed."})
			}
			continue
		}
		if strings.HasSuffix(path, ".py") {
			objects = append(objects, fields[2])
			paths = append(paths, path)
		}
	}
	if len(paths) > api.RouteImpactMaxPythonFiles {
		return sourceSnapshot{}, errors.New("source snapshot exceeds the Python file limit")
	}
	if err := r.readBlobs(ctx, &snapshot, objects, paths); err != nil {
		return sourceSnapshot{}, err
	}
	fingerprint(&snapshot)
	return snapshot, nil
}

func (r repository) readBlobs(ctx context.Context, snapshot *sourceSnapshot, objects, paths []string) error {
	if len(objects) == 0 {
		return nil
	}
	// Query sizes before fetching bodies so oversized blobs cannot allocate an
	// unbounded buffer. Object IDs came from Git, not caller text.
	input := []byte(strings.Join(objects, "\n") + "\n")
	headers, err := runGit(ctx, r.root, input, "cat-file", "--batch-check")
	if err != nil {
		return err
	}
	total := 0
	for _, row := range strings.Split(strings.TrimSpace(string(headers)), "\n") {
		var object, kind string
		var size int
		if _, err := fmt.Sscanf(row, "%s %s %d", &object, &kind, &size); err != nil || kind != "blob" || size < 0 {
			return errors.New("git returned invalid blob metadata")
		}
		if size > api.RouteImpactFileMaxBytes || size > api.RouteImpactSourceMaxBytes-total {
			return errors.New("source exceeds the route impact byte limit")
		}
		total += size
	}
	data, err := runGit(ctx, r.root, input, "cat-file", "--batch")
	if err != nil {
		return err
	}
	reader := bufio.NewReader(bytes.NewReader(data))
	for i, path := range paths {
		header, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("read Python blob header: %w", err)
		}
		var object, kind string
		var size int
		if _, err := fmt.Sscanf(header, "%s %s %d", &object, &kind, &size); err != nil || object != objects[i] || kind != "blob" || size < 0 || size > api.RouteImpactFileMaxBytes {
			return errors.New("git returned invalid blob contents")
		}
		content := make([]byte, size)
		if _, err := io.ReadFull(reader, content); err != nil {
			return fmt.Errorf("read Python blob: %w", err)
		}
		if delimiter, err := reader.ReadByte(); err != nil || delimiter != '\n' {
			return errors.New("git returned an invalid blob delimiter")
		}
		file := snapshot.files[path]
		file.body = content
		file.hash = contentHash(content)
		snapshot.files[path] = file
	}
	return nil
}

func (r repository) worktree(ctx context.Context) (sourceSnapshot, error) {
	paths, err := runGit(ctx, r.root, nil, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return sourceSnapshot{}, err
	}
	snapshot := sourceSnapshot{meta: Snapshot{Revision: "working-tree"}, files: map[string]sourceFile{}}
	// Start from HEAD metadata to detect all tracked non-Python file changes
	// without reading their contents (which may contain secrets).
	head, err := r.commit(ctx, "HEAD")
	if err != nil {
		return sourceSnapshot{}, err
	}
	tree, err := r.tree(ctx, head)
	if err != nil {
		return sourceSnapshot{}, err
	}
	for path, file := range tree.files {
		if !strings.HasSuffix(path, ".py") {
			snapshot.files[path] = file
		}
	}
	diff, err := runGit(ctx, r.root, nil, "diff", "--no-ext-diff", "--no-textconv", "--name-only", "-z", head, "--")
	if err != nil {
		return sourceSnapshot{}, err
	}
	for _, name := range bytes.Split(diff, []byte{0}) {
		if path, scoped := r.relative(string(name)); scoped && !strings.HasSuffix(path, ".py") {
			delete(snapshot.files, path)
			snapshot.files[path] = sourceFile{path: path, hash: "working-tree-change", mode: "modified"}
		}
	}
	total := 0
	seen := map[string]bool{}
	for _, name := range bytes.Split(paths, []byte{0}) {
		path, scoped := r.relative(string(name))
		if !scoped || path == "" || seen[path] {
			continue
		}
		seen[path] = true
		if _, exists := snapshot.files[path]; !exists && !strings.HasSuffix(path, ".py") {
			snapshot.files[path] = sourceFile{path: path, hash: "untracked", mode: "untracked"}
		}
		if len(snapshot.files) > api.RouteImpactMaxPaths {
			return sourceSnapshot{}, errors.New("working tree exceeds the route impact path limit")
		}
		if !strings.HasSuffix(path, ".py") {
			continue
		}
		file, err := r.readWorktreePython(string(name), path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return sourceSnapshot{}, err
		}
		snapshot.files[path] = file
		if file.mode == "120000" {
			snapshot.issues = append(snapshot.issues, Issue{Code: "unsupported_source_entry", File: path,
				Message: "Symlinked Python sources are not analyzed."})
			continue
		}
		total += len(file.body)
		snapshot.meta.PythonFiles++
		if snapshot.meta.PythonFiles > api.RouteImpactMaxPythonFiles || total > api.RouteImpactSourceMaxBytes {
			return sourceSnapshot{}, errors.New("working tree exceeds the Python source limit")
		}
	}
	// Deleted non-Python files must remain absent, rather than a change marker.
	for path := range snapshot.files {
		if strings.HasSuffix(path, ".py") {
			continue
		}
		if _, err := os.Lstat(filepath.Join(r.root, filepath.FromSlash(r.scope), filepath.FromSlash(path))); errors.Is(err, os.ErrNotExist) {
			delete(snapshot.files, path)
		}
	}
	fingerprint(&snapshot)
	return snapshot, nil
}

func (r repository) readWorktreePython(repoPath, path string) (sourceFile, error) {
	// os.Root prevents directory symlinks from escaping the repository.
	root, err := os.OpenRoot(r.root)
	if err != nil {
		return sourceFile{}, fmt.Errorf("open repository source root: %w", err)
	}
	defer func() { _ = root.Close() }()
	// Reject symlinked parent directories as well as symlinked final files.
	// os.Root independently prevents traversal outside the repository.
	parts := strings.Split(filepath.ToSlash(repoPath), "/")
	for i := 1; i < len(parts); i++ {
		parent, err := root.Lstat(filepath.FromSlash(strings.Join(parts[:i], "/")))
		if err != nil {
			return sourceFile{}, fmt.Errorf("inspect Python source parent: %w", err)
		}
		if parent.Mode()&os.ModeSymlink != 0 {
			return sourceFile{path: path, mode: "120000", hash: "symlink"}, nil
		}
	}
	info, err := root.Lstat(repoPath)
	if err != nil {
		return sourceFile{}, fmt.Errorf("inspect Python source: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return sourceFile{path: path, mode: "120000", hash: "symlink"}, nil
	}
	if !info.Mode().IsRegular() || info.Size() > api.RouteImpactFileMaxBytes {
		return sourceFile{}, errors.New("source file is not a bounded regular file")
	}
	file, err := root.Open(repoPath)
	if err != nil {
		return sourceFile{}, fmt.Errorf("open Python source: %w", err)
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, api.RouteImpactFileMaxBytes+1))
	if err != nil {
		return sourceFile{}, fmt.Errorf("read Python source: %w", err)
	}
	if len(body) > api.RouteImpactFileMaxBytes {
		return sourceFile{}, errors.New("source exceeds the file byte limit")
	}
	mode := "100644"
	if info.Mode().Perm()&0o111 != 0 {
		mode = "100755"
	}
	return sourceFile{path: path, mode: mode, hash: contentHash(body), body: body}, nil
}

func contentHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func fingerprint(snapshot *sourceSnapshot) {
	var paths []string
	for path, file := range snapshot.files {
		if file.body != nil {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	hash := sha256.New()
	for _, path := range paths {
		_, _ = fmt.Fprintf(hash, "%d:%s:%s\n", len(path), path, snapshot.files[path].hash)
	}
	snapshot.meta.SourceSHA256 = hex.EncodeToString(hash.Sum(nil))
	snapshot.meta.PythonFiles = len(paths)
}
