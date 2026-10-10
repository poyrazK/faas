package main

// copy-out (ADR-958): `gregale app cp` streams one path out of a fresh task
// VM as a tar archive. guest-init re-executes itself as the app user to
// produce it, so the copy can read exactly what the app user can.

import (
	"archive/tar"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// appTaskCopyOutHelperArg0 is the argv[0] that selects the copy-out helper
// in a re-executed guest-init child.
const appTaskCopyOutHelperArg0 = "guest-init-copy-out"

// writeCopyOutTar archives source (a file, directory, or symlink to one)
// under its base name. Symlinks below source are stored as links, never
// followed; sockets, devices and pipes are skipped.
func writeCopyOutTar(w io.Writer, source string) error {
	source = filepath.Clean(source)
	walkRoot, err := filepath.EvalSymlinks(source)
	if err != nil {
		return fmt.Errorf("%s: %w", source, err)
	}
	base := filepath.Base(source)
	if base == string(filepath.Separator) || base == "." {
		base = "root"
	}
	tw := tar.NewWriter(w)
	walkErr := filepath.WalkDir(walkRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(walkRoot, path)
		if err != nil {
			return err
		}
		name := base
		if rel != "." {
			name = base + "/" + filepath.ToSlash(rel)
		}
		return writeCopyOutEntry(tw, path, name, entry)
	})
	if walkErr != nil {
		return walkErr
	}
	return tw.Close()
}

func writeCopyOutEntry(tw *tar.Writer, path, name string, entry fs.DirEntry) error {
	info, err := entry.Info()
	if err != nil {
		return err
	}
	link := ""
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		if link, err = os.Readlink(path); err != nil {
			return err
		}
	case info.IsDir(), info.Mode().IsRegular():
	default:
		return nil
	}
	header, err := tar.FileInfoHeader(info, link)
	if err != nil {
		return err
	}
	header.Name = name
	if info.IsDir() && !strings.HasSuffix(header.Name, "/") {
		header.Name += "/"
	}
	header.Uname, header.Gname = "", ""
	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	//nolint:forbidigo // the app user's own files, read with the app user's credentials
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	_, err = io.Copy(tw, file)
	return err
}

// runCopyOutHelper is the body of the re-executed child: write the tar to
// stdout and report failures on stderr.
func runCopyOutHelper(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "copy-out: exactly one path is required")
		return 2
	}
	if err := writeCopyOutTar(os.Stdout, args[0]); err != nil {
		fmt.Fprintf(os.Stderr, "copy-out: %v\n", err)
		return 1
	}
	return 0
}
