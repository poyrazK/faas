package e2etest

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// CallerDir returns the source directory, in this checkout, of the package
// whose code calls it. runtime.Caller names a source file by its absolute path
// only without -trimpath. Local builds use -trimpath (the Makefile's
// GO_DEV_GOFLAGS) so that every checkout on a machine shares one copy of each
// package in the Go build cache; the file is then named by its package's
// import path, which `go list` maps back to a directory of the module that
// contains the working directory.
func CallerDir() (string, error) {
	_, file, _, ok := runtime.Caller(1)
	if !ok {
		return "", errors.New("e2etest: cannot locate the caller's source")
	}
	dir := filepath.Dir(file)
	if filepath.IsAbs(dir) {
		return dir, nil
	}
	out, err := exec.Command("go", "list", "-f", "{{.Dir}}", filepath.ToSlash(dir)).Output()
	if err != nil {
		return "", fmt.Errorf("e2etest: locate %s with go list: %w", dir, err)
	}
	return strings.TrimSpace(string(out)), nil
}
