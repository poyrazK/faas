package main

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
)

// pinGoToolEnv keeps the go tool's caches where the developer configured them
// before TestMain moves HOME into the sandbox. `go env -w` stores GOCACHE and
// GOTOOLCHAIN under the real home, so the tests that run `go list` and `go
// build` lost both, rebuilt into a cold cache and downloaded modules into the
// sandbox, whose read-only module tree os.RemoveAll then left behind: about
// 0.7 GB per run filled a developer disk (Hunt 14 H14-45). A helper
// subprocess inherits the pinned values.
func pinGoToolEnv() {
	if os.Getenv("GOCACHE") != "" && os.Getenv("GOMODCACHE") != "" {
		return
	}
	out, err := exec.Command("go", "env", "-json", "GOCACHE", "GOMODCACHE", "GOTOOLCHAIN").Output()
	if err != nil {
		return
	}
	var values map[string]string
	if json.Unmarshal(out, &values) != nil {
		return
	}
	for key, value := range values {
		if value != "" && os.Getenv(key) == "" {
			_ = os.Setenv(key, value)
		}
	}
}

// removeCLITestSandbox deletes the sandbox even when a test left a read-only
// tree in it (a Go module cache), which os.RemoveAll alone cannot unlink.
func removeCLITestSandbox(dir string) {
	if os.RemoveAll(dir) == nil {
		return
	}
	_ = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && entry.IsDir() {
			_ = os.Chmod(path, 0o700)
		}
		return nil
	})
	_ = os.RemoveAll(dir)
}
