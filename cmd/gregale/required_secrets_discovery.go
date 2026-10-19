package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Use the same root as linked defaults and accepted-run records. An explicit
// requirements selector bypasses discovery entirely, including malformed local
// project metadata. Missing declarations do not imply an empty requirement set.
func discoverRequiredSecretFile() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("read current directory: %w", err)
	}
	root, err := projectContextRoot(cwd)
	if err != nil {
		return "", fmt.Errorf("locate requirement declaration root: %w", err)
	}
	return requiredSecretFileAt(root)
}

func requiredSecretFileAt(root string) (string, error) {
	directory := filepath.Join(root, projectContextDirName)
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect declaration directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("declaration directory must be a real directory")
	}
	path := filepath.Join(directory, "required-secrets.json")
	info, err = os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect requirement declaration: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("requirement declaration must be a regular file, not a symlink or directory")
	}
	return path, nil
}
