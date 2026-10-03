package fcvm

// adr: 435. These records authorize cleanup only, never artifact admission.

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/gofrs/flock"
)

const runtimeSourceRootPrefix = "faas-runtime-sources-"
const runtimeSourceFormat = "gregale-native-runtime-sources-v1\n"
const runtimeSourceRecordMaxBytes = 1024

type runtimeSourceOwner struct {
	Version    int    `json:"version"`
	InstanceID string `json:"instance_id"`
}

// WithRuntimeSourceRoot selects a node-local durable parent outside jail tmpfs.
// Configure it before admitting work; a private process directory holds the
// shared sealed bytes and an OS lock held until its last consumer is released.
func (v *JailerVMM) WithRuntimeSourceRoot(root string) *JailerVMM {
	v.runtimeSourceRoot = root
	return v
}

func runtimeSourceOwnerName(instance string) string {
	return fmt.Sprintf("owner-%x.json", sha256.Sum256([]byte(instance)))
}

func (c *runtimeSourceCache) ensureRootLocked() error {
	if c.root != "" {
		return nil
	}
	if c.parent != "" {
		if err := prepareRuntimeSourceParent(c.parent); err != nil {
			return err
		}
	}
	root, err := os.MkdirTemp(c.parent, runtimeSourceRootPrefix)
	if err != nil {
		return err
	}
	lock := flock.New(filepath.Join(root, ".lock"))
	locked, err := lock.TryLock()
	if err == nil && !locked {
		err = errors.New("fcvm: new runtime source root is already locked")
	}
	if err == nil {
		err = writeRuntimeSourceRecord(root, ".format", []byte(runtimeSourceFormat))
	}
	if err != nil {
		return errors.Join(err, lock.Close(), os.RemoveAll(root))
	}
	c.root, c.lock = root, lock
	return nil
}

func prepareRuntimeSourceParent(parent string) error {
	if !filepath.IsAbs(parent) {
		return errors.New("fcvm: runtime source parent must be absolute")
	}
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(parent)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 || !runtimeSourceOwned(info) {
		return errors.New("fcvm: runtime source parent must be a private owned directory")
	}
	return syncRuntimeSourceDirectory(parent)
}

func runtimeSourceOwned(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}

func (c *runtimeSourceCache) retainOwner(instance string) error {
	if err := c.ensureRootLocked(); err != nil {
		return err
	}
	if c.owners[instance] {
		return nil
	}
	body, err := json.Marshal(runtimeSourceOwner{Version: 1, InstanceID: instance})
	if err != nil {
		return err
	}
	if err := writeRuntimeSourceRecord(c.root, runtimeSourceOwnerName(instance), body); err != nil {
		return err
	}
	c.owners[instance] = true
	return nil
}

func (c *runtimeSourceCache) releaseOwner(instance string) error {
	if c.root == "" {
		return nil
	}
	err := os.Remove(filepath.Join(c.root, runtimeSourceOwnerName(instance)))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	delete(c.owners, instance)
	return syncRuntimeSourceDirectory(c.root)
}

func writeRuntimeSourceRecord(root, name string, body []byte) error {
	if len(body) > runtimeSourceRecordMaxBytes {
		return errors.New("fcvm: oversized runtime source record")
	}
	file, err := os.CreateTemp(root, "owner-write-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(body)
	if err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		return err
	}
	if err := os.Rename(file.Name(), filepath.Join(root, name)); err != nil {
		return err
	}
	return syncRuntimeSourceDirectory(root)
}

func syncRuntimeSourceDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
