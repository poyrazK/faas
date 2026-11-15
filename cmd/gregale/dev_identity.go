package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const developerIDEnvironment = "FAAS_DEVELOPER_ID"

// developerIDPath holds a non-secret, installation-scoped identifier. It is
// intentionally separate from the authentication token: logging out should
// not make every local developer environment unreachable.
func developerIDPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "gregale", "developer-id"), nil
}

func validDeveloperID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, ch := range id {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

func readDeveloperID(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(b))
	if !validDeveloperID(id) {
		return "", fmt.Errorf("invalid developer ID in %s", path)
	}
	return id, nil
}

func loadOrCreateDeveloperID() (string, error) {
	if raw, ok := os.LookupEnv(developerIDEnvironment); ok {
		id := strings.TrimSpace(raw)
		if !validDeveloperID(id) {
			return "", fmt.Errorf("%s must be 32 lowercase hexadecimal characters", developerIDEnvironment)
		}
		return id, nil
	}

	path, err := developerIDPath()
	if err != nil {
		return "", err
	}
	if id, err := readDeveloperID(path); err == nil {
		return id, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}

	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate developer ID: %w", err)
	}
	id := hex.EncodeToString(random)
	file, err := os.CreateTemp(filepath.Dir(path), ".developer-id-*")
	if err != nil {
		return "", err
	}
	temporaryPath := file.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return "", err
	}
	if _, err := file.WriteString(id + "\n"); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	// Linking a fully written temporary file publishes the ID atomically and
	// never replaces a winner from another concurrent CLI process.
	if err := os.Link(temporaryPath, path); errors.Is(err, os.ErrExist) {
		return readDeveloperID(path)
	} else if err != nil {
		return "", err
	}
	return id, nil
}

// onDiskPathCase rewrites each component of an absolute path to the spelling
// stored on disk on the case-insensitive file systems macOS and Windows use
// by default. EvalSymlinks keeps the spelling it was given, so `cd ~/Code/app`
// and `cd ~/code/app` reached one directory but derived two workspaces, and
// a second developer environment. A component that cannot be read keeps
// its spelling.
func onDiskPathCase(path string) string {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		return path
	}
	volume := filepath.VolumeName(path)
	rest := strings.TrimPrefix(path[len(volume):], string(filepath.Separator))
	if rest == "" {
		return path
	}
	current := volume + string(filepath.Separator)
	for _, component := range strings.Split(rest, string(filepath.Separator)) {
		name := component
		if entries, err := os.ReadDir(current); err == nil {
			for _, entry := range entries {
				if entry.Name() == component {
					name = component
					break
				}
				if strings.EqualFold(entry.Name(), component) {
					name = entry.Name()
				}
			}
		}
		current = filepath.Join(current, name)
	}
	return current
}

// deriveDevWorkspaceID scopes a developer environment to this CLI
// installation and canonical source directory without disclosing either value
// to the API. Separate clones and worktrees intentionally get separate URLs.
func deriveDevWorkspaceID(developerID, sourceDir string) (string, error) {
	if !validDeveloperID(developerID) {
		return "", errors.New("invalid developer ID")
	}
	abs, err := filepath.Abs(sourceDir)
	if err != nil {
		return "", fmt.Errorf("resolve developer source: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("canonicalize developer source: %w", err)
	}
	canonical = onDiskPathCase(filepath.Clean(canonical))
	sum := sha256.Sum256([]byte(developerID + "\x00" + filepath.Clean(canonical)))
	return hex.EncodeToString(sum[:16]), nil
}
