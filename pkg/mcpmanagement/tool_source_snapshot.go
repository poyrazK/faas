package mcpmanagement

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Freeze reviewed source before the asynchronous CLI can read it. Edits to the
// agent's working directory after apply must not alter either candidate build.
func snapshotToolSource(source, digest string) (snapshot string, err error) {
	snapshot, err = os.MkdirTemp("", "gregale-tool-source-")
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			_ = removeToolSnapshot(snapshot)
		}
	}()
	root, err := os.OpenRoot(source)
	if err != nil {
		return snapshot, err
	}
	defer func() { _ = root.Close() }()
	project := filepath.Join(snapshot, "project")
	if err = os.Mkdir(project, 0o700); err != nil {
		return snapshot, err
	}
	type directoryMode struct {
		path string
		mode fs.FileMode
	}
	directories := []directoryMode{}
	var total int64
	files := 0
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == ".git" && entry.IsDir() {
			return fs.SkipDir
		}
		target := filepath.Join(project, filepath.FromSlash(name))
		if entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			directories = append(directories, directoryMode{target, info.Mode().Perm()})
			if name == "." {
				return nil
			}
			return os.Mkdir(target, 0o700)
		}
		files++
		if files > maxLocalSourceFiles {
			return invalid("source_too_large", "publication snapshot exceeds the local file bound")
		}
		n, err := copyToolSourceFile(root, name, target, toolProjectHashBytes-total)
		total += n
		return err
	})
	if err == nil {
		for i := len(directories) - 1; i >= 0; i-- {
			if err = os.Chmod(directories[i].path, directories[i].mode); err != nil {
				break
			}
		}
	}
	if err == nil {
		var actual string
		actual, err = toolSourceDigest(project)
		if err == nil && actual != digest {
			err = invalid("source_changed", "source changed while creating the reviewed publication snapshot")
		}
	}
	return snapshot, err
}

// Restoring source directory permissions can make them read-only. Reopen only
// our private snapshot directories for cleanup; WalkDir never follows symlinks.
func removeToolSnapshot(root string) error {
	if root == "" {
		return nil
	}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return os.Chmod(path, 0o700)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return os.RemoveAll(root)
}

func copyToolSourceFile(root *os.Root, name, target string, remaining int64) (int64, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() || info.Size() > remaining {
		return 0, invalid("invalid_project", "publication snapshots require bounded regular source files")
	}
	in, err := root.Open(name)
	if err != nil {
		return 0, err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	n, copyErr := io.Copy(out, io.LimitReader(in, info.Size()+1))
	modeErr := out.Chmod(info.Mode().Perm())
	closeErr := out.Close()
	if copyErr != nil {
		return n, copyErr
	}
	if modeErr != nil {
		return n, modeErr
	}
	if closeErr != nil {
		return n, closeErr
	}
	if n != info.Size() {
		return n, invalid("source_changed", "source file changed while copying the publication snapshot")
	}
	return n, nil
}
