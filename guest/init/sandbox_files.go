package main

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/onebox-faas/faas/pkg/sandboxproto"
)

// sandboxFileMode is a file the files API creates without a mode.
const sandboxFileMode fs.FileMode = 0o644

// sandboxDirMode is a directory the files API or an archive creates.
const sandboxDirMode fs.FileMode = 0o755

// openWorkspace opens the workspace root and resolves p in it.
func (s *sandboxServer) openWorkspace(field, p string) (*os.Root, string, error) {
	rel, ok := sandboxproto.WorkspacePath(s.workspace, p)
	if !ok {
		return nil, "", sandboxError(sandboxproto.CodeOutsideWorkspace, "%s %q is outside %s", field, p, s.workspace)
	}
	root, err := os.OpenRoot(s.workspace)
	if err != nil {
		return nil, "", &sandboxproto.GuestError{Code: sandboxproto.CodeInternal, Message: "the workspace is unavailable"}
	}
	return root, rel, nil
}

// fileOp runs a files operation.
func (s *sandboxServer) fileOp(req sandboxproto.Request, call *sandboxproto.Call) (any, error) {
	root, rel, err := s.openWorkspace("path", req.Path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	switch req.Op {
	case sandboxproto.OpFileRead:
		return s.fileRead(root, rel, req.MaxBytes, call)
	case sandboxproto.OpFileWrite:
		return s.fileWrite(root, rel, req, call)
	case sandboxproto.OpFileList:
		return s.fileList(root, rel, req.Depth)
	case sandboxproto.OpFileStat:
		info, err := root.Lstat(rel)
		if err != nil {
			return nil, fileError(rel, err)
		}
		return s.fileInfo(root, rel, info), nil
	case sandboxproto.OpFileRemove:
		return s.fileRemove(root, rel, req.Recursive)
	case sandboxproto.OpFileMkdir:
		return s.fileMkdir(root, rel, req)
	case sandboxproto.OpFileRename:
		to, ok := sandboxproto.WorkspacePath(s.workspace, req.To)
		if !ok || to == "." || rel == "." {
			return nil, sandboxError(sandboxproto.CodeOutsideWorkspace, "a rename stays inside %s and leaves its root in place", s.workspace)
		}
		if err := root.Rename(rel, to); err != nil {
			return nil, fileError(rel, err)
		}
		return struct{}{}, nil
	}
	return nil, sandboxError(sandboxproto.CodeUnsupported, "%s is not a files operation", req.Op)
}

// fileRead streams a regular file's contents, at most maxBytes when set.
func (s *sandboxServer) fileRead(root *os.Root, rel string, maxBytes int64, call *sandboxproto.Call) (any, error) {
	f, err := root.Open(rel)
	if err != nil {
		return nil, fileError(rel, err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, fileError(rel, err)
	}
	if info.IsDir() {
		return nil, sandboxError(sandboxproto.CodeIsADirectory, "%s is a directory", rel)
	}
	if !info.Mode().IsRegular() {
		return nil, sandboxError(sandboxproto.CodeUnsupported, "%s is not a regular file", rel)
	}
	var r io.Reader = f
	if maxBytes > 0 {
		r = io.LimitReader(f, maxBytes)
	}
	n, err := io.Copy(call.Data(), r)
	if err != nil {
		return nil, err
	}
	return sandboxproto.TransferResult{Bytes: n, Truncated: maxBytes > 0 && info.Size() > n}, nil
}

// fileWrite replaces a file with the uploaded contents. With parents, the
// missing directories above it are created.
func (s *sandboxServer) fileWrite(root *os.Root, rel string, req sandboxproto.Request, call *sandboxproto.Call) (any, error) {
	if rel == "." {
		return nil, sandboxError(sandboxproto.CodeIsADirectory, "the workspace root is a directory")
	}
	if req.Parents {
		if err := s.mkdirAll(root, path.Dir(rel)); err != nil {
			return nil, err
		}
	}
	mode := sandboxFileMode
	if req.Mode != 0 {
		mode = fs.FileMode(req.Mode)
	}
	f, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return nil, fileError(rel, err)
	}
	limit := int64(sandboxproto.MaxWriteBytes)
	if req.MaxBytes > 0 {
		limit = req.MaxBytes
	}
	n, err := call.ReadUpload(f, limit)
	if closeErr := f.Close(); err == nil && closeErr != nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	if err := s.chown(root, rel); err != nil {
		return nil, fileError(rel, err)
	}
	return sandboxproto.TransferResult{Bytes: n}, nil
}

// fileList lists rel's entries, depth levels down (one when unset). Links
// are listed, never followed.
func (s *sandboxServer) fileList(root *os.Root, rel string, depth int) (any, error) {
	info, err := root.Stat(rel)
	if err != nil {
		return nil, fileError(rel, err)
	}
	if !info.IsDir() {
		return nil, sandboxError(sandboxproto.CodeNotADirectory, "%s is not a directory", rel)
	}
	if depth == 0 {
		depth = 1
	}
	out := sandboxproto.FileList{Entries: []sandboxproto.FileInfo{}}
	base := strings.Count(rel, "/") + 1
	if rel == "." {
		base = 0
	}
	err = fs.WalkDir(root.FS(), rel, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == rel {
			return nil
		}
		level := strings.Count(name, "/") + 1 - base
		if len(out.Entries) == sandboxproto.MaxListEntries {
			out.Truncated = true
			return fs.SkipAll
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		out.Entries = append(out.Entries, s.fileInfo(root, name, info))
		if entry.IsDir() && level >= depth {
			return fs.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, fileError(rel, err)
	}
	return out, nil
}

func (s *sandboxServer) fileInfo(root *os.Root, name string, info fs.FileInfo) sandboxproto.FileInfo {
	out := sandboxproto.FileInfo{Path: name, Size: info.Size(), Mode: uint32(info.Mode().Perm()), ModUnixMs: info.ModTime().UnixMilli()}
	switch mode := info.Mode(); {
	case mode.IsRegular():
		out.Type = sandboxproto.TypeFile
	case mode.IsDir():
		out.Type = sandboxproto.TypeDir
	case mode&fs.ModeSymlink != 0:
		out.Type = sandboxproto.TypeSymlink
		out.LinkTarget, _ = root.Readlink(name)
	default:
		out.Type = sandboxproto.TypeOther
	}
	return out
}

func (s *sandboxServer) fileRemove(root *os.Root, rel string, recursive bool) (any, error) {
	if rel == "." {
		return nil, sandboxError(sandboxproto.CodeInvalidRequest, "the workspace root cannot be removed")
	}
	if _, err := root.Lstat(rel); err != nil {
		return nil, fileError(rel, err)
	}
	remove := root.Remove
	if recursive {
		remove = root.RemoveAll
	}
	if err := remove(rel); err != nil {
		return nil, fileError(rel, err)
	}
	return struct{}{}, nil
}

func (s *sandboxServer) fileMkdir(root *os.Root, rel string, req sandboxproto.Request) (any, error) {
	if req.Parents {
		if err := s.mkdirAll(root, rel); err != nil {
			return nil, err
		}
		return struct{}{}, nil
	}
	mode := sandboxDirMode
	if req.Mode != 0 {
		mode = fs.FileMode(req.Mode)
	}
	if err := root.Mkdir(rel, mode); err != nil {
		return nil, fileError(rel, err)
	}
	if err := s.chown(root, rel); err != nil {
		return nil, fileError(rel, err)
	}
	return struct{}{}, nil
}

// mkdirAll creates rel and its missing parents, each owned like the files
// the API writes.
func (s *sandboxServer) mkdirAll(root *os.Root, rel string) error {
	if rel == "." {
		return nil
	}
	parts := strings.Split(rel, "/")
	for i := range parts {
		name := strings.Join(parts[:i+1], "/")
		info, err := root.Stat(name)
		if err == nil {
			if !info.IsDir() {
				return sandboxError(sandboxproto.CodeNotADirectory, "%s is not a directory", name)
			}
			continue
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return fileError(name, err)
		}
		if err := root.Mkdir(name, sandboxDirMode); err != nil && !errors.Is(err, fs.ErrExist) {
			return fileError(name, err)
		}
		if err := s.chown(root, name); err != nil {
			return fileError(name, err)
		}
	}
	return nil
}

// chown gives name to the session's user, so commands that run as the user
// can change what the files API wrote.
func (s *sandboxServer) chown(root *os.Root, name string) error {
	if s.owner < 0 {
		return nil
	}
	return root.Lchown(name, s.owner, s.owner)
}
