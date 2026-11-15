package mcpmanagement

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unicode/utf8"
)

const (
	maxAssessmentFiles     = 2000
	maxAssessmentFileBytes = 256 << 10
	maxAssessmentBytes     = 8 << 20
	maxAssessmentWorkloads = 50
)

// zip.Reader provides a standard, read-only filesystem over a bounded source
// snapshot, including directory traversal, without creating temporary files.
type assessmentFS struct{ *zip.Reader }

func assessmentSourceFS(files map[string]string) (assessmentFS, error) {
	var data bytes.Buffer
	w := zip.NewWriter(&data)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		f, err := w.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			return assessmentFS{}, err
		}
		if _, err := io.WriteString(f, files[name]); err != nil {
			return assessmentFS{}, err
		}
	}
	if err := w.Close(); err != nil {
		return assessmentFS{}, err
	}
	r, err := zip.NewReader(bytes.NewReader(data.Bytes()), int64(data.Len()))
	return assessmentFS{r}, err
}

func excludedAssessmentPath(name string) bool {
	for _, p := range strings.Split(name, "/") {
		switch p {
		case ".git", "node_modules", "vendor", ".venv", "venv", ".next", "dist", "build", ".cache":
			return true
		}
	}
	base := strings.ToLower(path.Base(name))
	return base == ".env" || strings.HasPrefix(base, ".env.") || strings.HasSuffix(base, ".pem") || strings.HasSuffix(base, ".key") || base == "runtime.env" || base == "credentials.json"
}

func (m *management) assessmentFiles(ctx context.Context, a assessProjectArgs) (map[string]string, error) {
	if (a.Path == "") == (a.Files == nil) {
		return nil, invalid("invalid_argument", "supply exactly one of path or files")
	}
	if a.Files != nil {
		if len(a.Files) == 0 || len(a.Files) > maxInlineFiles {
			return nil, invalid("assessment_too_large", "supply 1 to 200 source files")
		}
		total := 0
		for name, data := range a.Files {
			total += len(name) + len(data)
			if !fs.ValidPath(name) || name == "." || strings.ContainsAny(name, "\\\x00") || len(name) > maxUploadPath || excludedAssessmentPath(name) || !utf8.ValidString(data) {
				return nil, invalid("invalid_source", "use relative UTF-8 source files without traversal, secrets or generated directories")
			}
			for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
				if _, exists := a.Files[parent]; exists {
					return nil, invalid("invalid_source", "a source file cannot also be a directory")
				}
			}
		}
		if total > maxInlineBytes {
			return nil, invalid("assessment_too_large", "inline source exceeds 96 KiB; select relevant files")
		}
		return a.Files, ctx.Err()
	}
	// A caller's path must never select files on the hosted bridge.
	if m.remote() || m.opts.LocalCLI == nil || m.opts.AllowedApps != nil {
		return nil, invalid("unsupported_source", "this connection accepts files only; never supply a path on the hosted bridge")
	}
	dir := filepath.Clean(a.Path)
	if !filepath.IsAbs(a.Path) || dir == filepath.Dir(dir) {
		return nil, invalid("invalid_path", "select an absolute project directory")
	}
	home, _ := os.UserHomeDir()
	if home != "" && (dir == home || strings.HasPrefix(home, dir+string(filepath.Separator))) {
		return nil, invalid("invalid_path", "select a project below the home directory")
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return nil, invalid("invalid_path", "project must be an existing directory, not a symbolic link")
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil || resolved == filepath.Dir(resolved) || home != "" && (resolved == home || strings.HasPrefix(home, resolved+string(filepath.Separator))) {
		return nil, invalid("invalid_path", "select a project directory, not an alias of the root or home directory")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, invalid("invalid_path", "could not open the project directory")
	}
	defer func() { _ = root.Close() }()
	files, total, visited := map[string]string{}, 0, 0
	err = fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		visited++
		if visited > maxAssessmentFiles*2 {
			return invalid("assessment_too_large", "project has too many entries; supply selected files")
		}
		if name != "." && excludedAssessmentPath(name) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() || d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return invalid("invalid_source", "project contains a non-regular file")
		}
		if info.Size() > maxAssessmentFileBytes || total+int(info.Size()) > maxAssessmentBytes || len(files) >= maxAssessmentFiles {
			return invalid("assessment_too_large", "project exceeds assessment bounds; supply selected files")
		}
		f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			return err
		}
		opened, statErr := f.Stat()
		if statErr != nil || !opened.Mode().IsRegular() {
			_ = f.Close()
			return invalid("invalid_source", "project source must remain a regular file")
		}
		data, err := io.ReadAll(io.LimitReader(f, maxAssessmentFileBytes+1))
		_ = f.Close()
		if err != nil {
			return err
		}
		total += len(data)
		if len(data) > maxAssessmentFileBytes || total > maxAssessmentBytes {
			return invalid("assessment_too_large", "project exceeds assessment bounds")
		}
		if utf8.Valid(data) {
			files[name] = string(data)
		}
		return nil
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var problem *inputError
		if errors.As(err, &problem) {
			return nil, err
		}
		return nil, invalid("invalid_source", "could not read the project within assessment bounds")
	}
	return files, nil
}
