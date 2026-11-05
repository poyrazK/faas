//go:build linux

package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/unix"

	"github.com/onebox-faas/faas/pkg/api"
)

// secretFilesTmpfsBytes bounds the /run/secrets tmpfs: every file at the
// largest plan's value size, twice over for the replacement written beside it.
var secretFilesTmpfsBytes = func() int {
	limits, _ := api.LimitsFor(api.PlanScale)
	return api.SecretFilesMax * 2 * limits.SecretValueMaxBytes
}()

// secretFileWriter writes the main workload's Compose secrets to
// /run/secrets (ADR-20261023090000), owned by the workload's user. The
// directory is a tmpfs, so values stay in guest memory as they do in the
// process environment.
type secretFileWriter struct {
	dir      string
	files    []api.SecretFile
	uid, gid int
	mount    func(dir string) error
	log      *slog.Logger
}

// newSecretFileWriter returns nil when the manifest declares no secret files.
// root is the workload's image root; "" is the guest root.
func newSecretFileWriter(root string, manifest api.AppManifest, log *slog.Logger) (*secretFileWriter, error) {
	if len(manifest.SecretFiles) == 0 {
		return nil, nil
	}
	credential, err := processCredential(root, manifest.EffectiveUser())
	if err != nil {
		return nil, fmt.Errorf("resolve secret file owner: %w", err)
	}
	if root == "" {
		root = "/"
	}
	if log == nil {
		log = slog.Default()
	}
	return &secretFileWriter{
		dir: filepath.Join(root, api.SecretFilesDir), files: api.CloneSecretFiles(manifest.SecretFiles),
		uid: int(credential.Uid), gid: int(credential.Gid), mount: mountSecretFilesTmpfs, log: log,
	}, nil
}

// write replaces every file from secrets. A secret without a value has no
// file; its name, never a value, is logged.
func (w *secretFileWriter) write(secrets map[string]string) error {
	if w == nil {
		return nil
	}
	if err := w.mount(w.dir); err != nil {
		return fmt.Errorf("secret files: mount %s: %w", w.dir, err)
	}
	for _, file := range w.files {
		target := filepath.Join(w.dir, file.Name)
		value, ok := secrets[file.Secret]
		if !ok {
			w.log.Warn("guest-init: secret file has no value; set the app secret", "file", file.Name, "secret", file.Secret)
			if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("secret files: remove %s: %w", file.Name, err)
			}
			continue
		}
		if err := w.replace(target, file, value); err != nil {
			return fmt.Errorf("secret files: write %s: %w", file.Name, err)
		}
	}
	return nil
}

// replace writes beside the target and renames over it, so a reader sees the
// old value or the new one, never a partial file.
func (w *secretFileWriter) replace(target string, file api.SecretFile, value string) error {
	tmp, err := os.CreateTemp(w.dir, "."+file.Name+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if err := tmp.Chown(w.uid, w.gid); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(os.FileMode(file.EffectiveMode())); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(value); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, target)
}

// mountSecretFilesTmpfs mounts a tmpfs at dir unless one is already there,
// which also makes it safe to call on every write.
func mountSecretFilesTmpfs(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var stat unix.Statfs_t
	if err := unix.Statfs(dir, &stat); err == nil && stat.Type == unix.TMPFS_MAGIC {
		return nil
	}
	return unix.Mount("tmpfs", dir, "tmpfs", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC, "mode=0755,size="+strconv.Itoa(secretFilesTmpfsBytes))
}
