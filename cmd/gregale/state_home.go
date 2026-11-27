package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// gregaleStateHome is the directory under which the CLI keeps its local
// state (deploy sessions, resumable uploads, start sessions), each in its own
// gregale/<kind> subdirectory: XDG_STATE_HOME when set, else the per-user
// config directory.
//
// A coding agent's sandbox can leave the config directory read-only: Codex's
// workspace-write sandbox blocks ~/Library/Application Support, and every
// first deploy in the 2026-11-19 agent eval stopped with "Could not save
// deploy session" until the agent retried with XDG_STATE_HOME=$(mktemp -d).
// When the default is not writable, the state goes to a private per-user
// directory under the system temp directory instead, and fallbackFrom names
// the default that was skipped. An XDG_STATE_HOME the caller set is used as
// given, so a read-only one stays an error.
func gregaleStateHome() (dir, fallbackFrom string, err error) {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return dir, "", nil
	}
	config, err := os.UserConfigDir()
	if err == nil {
		if err = probeWritableDir(filepath.Join(config, "gregale")); err == nil {
			return config, "", nil
		}
		fallbackFrom = filepath.Join(config, "gregale")
	}
	tmp, tmpErr := tempStateHome()
	if tmpErr != nil {
		return "", "", fmt.Errorf("locate Gregale state directory: %w", errors.Join(err, tmpErr))
	}
	if fallbackFrom == "" {
		fallbackFrom = "the user config directory"
	}
	return tmp, fallbackFrom, nil
}

// tempStateHome is the fallback state home: <temp>/gregale-state-<uid>,
// created 0700. A directory that already exists must be this user's, private
// and not a symlink, so on a shared /tmp another user cannot plant it.
func tempStateHome() (string, error) {
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("gregale-state-%d", os.Getuid()))
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
		return "", fmt.Errorf("%s is not a directory", dir)
	}
	if err := checkPrivateStateDir(dir, info); err != nil {
		return "", err
	}
	if err := probeWritableDir(dir); err != nil {
		return "", err
	}
	return dir, nil
}

// probeWritableDir creates dir if needed and proves a file can be written
// in it, leaving nothing behind.
func probeWritableDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	probe, err := os.CreateTemp(dir, ".probe-*")
	if err != nil {
		return err
	}
	name := probe.Name()
	_ = probe.Close()
	return os.Remove(name)
}
