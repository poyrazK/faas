package main

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

// base captures shell and resource bindings before the file is layered on.
// Rebuilding from this snapshot lets removed file keys fall back correctly.
type devLocalEnvConfig struct {
	path string
	base []string
}

func resolveDevLocalEnvFile(cwd, root, path string, explicit bool) (string, error) {
	manifest, _, err := gregalemanifest.Load(root)
	if err != nil {
		return "", err
	}
	if manifest != nil && manifest.Dev != nil {
		if err := manifest.Dev.Validate(); err != nil {
			return "", err
		}
		if !explicit && manifest.Dev.EnvFile != "" {
			path = filepath.Join(root, manifest.Dev.EnvFile)
		}
	}
	return resolveDevEnvFilePath(cwd, path)
}

func (c devLocalEnvConfig) load() ([]string, error) {
	if c.path == "" {
		return append([]string(nil), c.base...), nil
	}
	// Validate the file type before reading, including after an editor rename.
	if _, err := devEnvFileFingerprint(c.path); err != nil {
		return nil, err
	}
	pairs, _, err := readDevEnvFileEntries(c.path)
	if err != nil {
		return nil, err
	}
	env := append([]string(nil), c.base...)
	for _, pair := range pairs {
		if strings.ContainsRune(pair.Key, 0) || strings.ContainsRune(pair.Value, 0) {
			return nil, fmt.Errorf("env file contains NUL bytes")
		}
		env = append(env, pair.Key+"="+pair.Value)
	}
	return env, nil
}

// Hash file contents independently of source ignore rules and its location.
// An unavailable file is also a change: report it once, keep serving, and
// retry when it is repaired. Neither contents nor hashes are printed.
func (c devLocalEnvConfig) fingerprint() [sha256.Size]byte {
	if c.path == "" {
		return [sha256.Size]byte{}
	}
	if _, err := devEnvFileFingerprint(c.path); err == nil {
		if data, err := readDevEnvBytes(c.path); err == nil {
			return sha256.Sum256(data)
		}
	}
	return sha256.Sum256([]byte("unavailable local env file"))
}

func (c devLocalEnvConfig) sourceFingerprint(root string) ([sha256.Size]byte, error) {
	source, err := devSourceFingerprint(root)
	if err != nil {
		return source, err
	}
	env := c.fingerprint()
	return sha256.Sum256(append(source[:], env[:]...)), nil
}
