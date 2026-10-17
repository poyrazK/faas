package main

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// readTree returns the regular files under dir (repo-relative, slash
// separated) whose names satisfy keep. ref "" reads the working tree, so a
// local run checks uncommitted edits; any other ref is read with git archive.
func readTree(repo, ref, dir string, keep func(name string) bool) (map[string][]byte, error) {
	files := map[string][]byte{}
	if ref == "" {
		root := filepath.Join(repo, filepath.FromSlash(dir))
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, err := filepath.Rel(repo, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if !keep(rel) {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			files[rel] = data
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk %s: %w", dir, err)
		}
		return files, nil
	}

	cmd := exec.Command("git", "-C", repo, "archive", "--format=tar", ref, "--", dir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("git archive: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("git archive: %w", err)
	}
	tr := tar.NewReader(out)
	var readErr error
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			readErr = fmt.Errorf("read git archive: %w", err)
			break
		}
		if hdr.Typeflag != tar.TypeReg || !keep(hdr.Name) {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			readErr = fmt.Errorf("read %s from git archive: %w", hdr.Name, err)
			break
		}
		files[hdr.Name] = data
	}
	_, _ = io.Copy(io.Discard, out)
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("git archive %s -- %s: %w: %s", ref, dir, err, strings.TrimSpace(stderr.String()))
	}
	if readErr != nil {
		return nil, readErr
	}
	return files, nil
}
