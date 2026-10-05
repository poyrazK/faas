//go:build linux

// adr: 435

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	runtimeSecretCurrentName      = "..current"
	runtimeSecretGenerationPrefix = "..generation-"
)

type runtimeSecretSnapshot struct {
	Revision string            `json:"revision"`
	Secrets  map[string]string `json:"secrets"`
}

func (p runtimeSecretProjection) publish(secrets map[string]string, revision string) error {
	return p.publishWithWriter(runtimeSecretSnapshot{Revision: revision, Secrets: cloneRuntimeSecrets(secrets)}, p.writeGeneration)
}

// Stage every file before switching one platform-owned pointer. The old
// generation and restart state remain current if any staging operation fails.
func (p runtimeSecretProjection) publishWithWriter(snapshot runtimeSecretSnapshot, writeGeneration func(string, runtimeSecretSnapshot) error) error {
	if snapshot.Revision != "" && !validGuestRuntimeSecretRevision(snapshot.Revision) {
		return errors.New("invalid runtime secret revision")
	}
	dir := filepath.Dir(p.secretsPath)
	secretName, revisionName := filepath.Base(p.secretsPath), filepath.Base(p.revisionPath)
	if dir != filepath.Dir(p.revisionPath) || secretName == revisionName {
		return errors.New("runtime secret projection paths must be distinct files in one directory")
	}
	for _, name := range []string{secretName, revisionName} {
		if name == "." || name == runtimeSecretCurrentName || name == secretReloadSnapshotFileName || strings.HasPrefix(name, runtimeSecretGenerationPrefix) {
			return errors.New("runtime secret projection filename is reserved")
		}
	}
	if err := secureRuntimeSecretDirectory(dir, p.dirUID, p.dirGID); err != nil {
		return err
	}
	current := filepath.Join(dir, runtimeSecretCurrentName)
	previous, err := os.Readlink(current)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("inspect runtime secret generation: %w", err)
	}
	if previous != "" && (filepath.Base(previous) != previous || !strings.HasPrefix(previous, runtimeSecretGenerationPrefix)) {
		return errors.New("runtime secret generation pointer is invalid")
	}
	names := []string{secretName, revisionName, secretReloadSnapshotFileName}
	for _, name := range names {
		if err := verifyRuntimeSecretAlias(dir, name); err != nil {
			return err
		}
	}
	generation, err := os.MkdirTemp(dir, runtimeSecretGenerationPrefix)
	if err != nil {
		return fmt.Errorf("stage runtime secret generation: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(generation)
		}
	}()
	if err := writeGeneration(generation, snapshot); err != nil {
		return fmt.Errorf("stage runtime secret files: %w", err)
	}
	for _, name := range names {
		alias := filepath.Join(dir, name)
		if _, err := os.Lstat(alias); os.IsNotExist(err) {
			if err := os.Symlink(filepath.Join(runtimeSecretCurrentName, name), alias); err != nil {
				return fmt.Errorf("install runtime secret file path: %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("inspect runtime secret file path: %w", err)
		}
	}
	link := generation + ".link"
	defer func() { _ = os.Remove(link) }()
	if err := os.Symlink(filepath.Base(generation), link); err != nil {
		return fmt.Errorf("stage runtime secret generation pointer: %w", err)
	}
	if err := os.Rename(link, current); err != nil {
		return fmt.Errorf("publish runtime secret generation: %w", err)
	}
	committed = true
	// Cleanup cannot turn a committed publication into a reported failure.
	// An open snapshot descriptor continues to refer to its immutable file.
	if previous != "" {
		_ = os.RemoveAll(filepath.Join(dir, previous))
	}
	return nil
}

func verifyRuntimeSecretAlias(dir, name string) error {
	alias := filepath.Join(dir, name)
	target, err := os.Readlink(alias)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect runtime secret file alias: %w", err)
	}
	if target != filepath.Join(runtimeSecretCurrentName, name) {
		return errors.New("runtime secret file alias is invalid")
	}
	return nil
}

func (p runtimeSecretProjection) writeGeneration(dir string, snapshot runtimeSecretSnapshot) error {
	if err := writeRuntimeSecretsProjectionForOwner(filepath.Join(dir, filepath.Base(p.secretsPath)), p.uid, p.dirUID, p.dirGID, snapshot.Secrets); err != nil {
		return err
	}
	if err := writeRuntimeSecretRevisionProjectionForOwner(filepath.Join(dir, filepath.Base(p.revisionPath)), p.uid, p.dirUID, p.dirGID, snapshot.Revision); err != nil {
		return err
	}
	body, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("encode runtime secret snapshot: %w", err)
	}
	return writePrivateRuntimeSecretFile(filepath.Join(dir, secretReloadSnapshotFileName), p.uid, p.dirUID, p.dirGID, body)
}

func secureRuntimeSecretDirectory(dir string, uid, gid int) error {
	if err := os.MkdirAll(dir, 0o711); err != nil {
		return fmt.Errorf("create runtime secret directory: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspect runtime secret directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("runtime secret path is not a directory")
	}
	if err := os.Chown(dir, uid, gid); err != nil {
		return fmt.Errorf("secure runtime secret directory owner: %w", err)
	}
	if err := os.Chmod(dir, 0o711); err != nil {
		return fmt.Errorf("secure runtime secret directory mode: %w", err)
	}
	return nil
}

func writePrivateRuntimeSecretFile(path string, uid, dirUID, dirGID int, body []byte) error {
	if err := secureRuntimeSecretDirectory(filepath.Dir(path), dirUID, dirGID); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".secret-*.tmp")
	if err != nil {
		return fmt.Errorf("create runtime secret temp file: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	defer func() { _ = tmp.Close() }()
	if err := tmp.Chown(uid, dirGID); err != nil {
		return fmt.Errorf("set runtime secret owner: %w", err)
	}
	if err := tmp.Chmod(0o400); err != nil {
		return fmt.Errorf("set runtime secret mode: %w", err)
	}
	if _, err := tmp.Write(body); err != nil {
		return fmt.Errorf("write runtime secret file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync runtime secret file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close runtime secret file: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("publish runtime secret file: %w", err)
	}
	return nil
}
