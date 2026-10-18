package objectnode

import (
	"errors"
	"os"
	"path/filepath"
)

// Recovery runs under the process lock, before serving traffic. Each object's
// durable current.json is authoritative; unfinished and superseded blobs are
// reclaimed only after that object's catalog has been validated.
func (s *Store) recover() error {
	for _, name := range []string{"staging", "trash"} {
		dir := filepath.Join(s.root, name)
		if err := visitDirectory(dir, func(entry os.DirEntry) error {
			return os.RemoveAll(filepath.Join(dir, entry.Name()))
		}); err != nil {
			return err
		}
	}
	return visitDirectory(filepath.Join(s.root, "buckets"), func(entry os.DirEntry) error {
		dir := filepath.Join(s.root, "buckets", entry.Name())
		var b bucketRecord
		err := readJSON(filepath.Join(dir, "bucket.json"), &b)
		if errors.Is(err, os.ErrNotExist) {
			return os.RemoveAll(dir)
		}
		if err != nil || nameHash(b.Name) != entry.Name() {
			return ErrCorrupt
		}
		if _, err = s.bucket(b.Name); err != nil {
			return err
		}
		return visitDirectory(filepath.Join(dir, "objects"), func(object os.DirEntry) error {
			return recoverObject(filepath.Join(dir, "objects", object.Name()))
		})
	})
}

func recoverObject(dir string) error {
	o, err := readObject(dir)
	if errors.Is(err, ErrNoObject) {
		return os.RemoveAll(dir)
	}
	if err != nil {
		return err
	}
	blob := o.ID + ".data"
	info, err := os.Stat(filepath.Join(dir, blob))
	if err != nil || !info.Mode().IsRegular() || info.Size() != o.Size {
		return ErrCorrupt
	}
	if err = visitDirectory(dir, func(entry os.DirEntry) error {
		if entry.Name() == "current.json" || entry.Name() == blob {
			return nil
		}
		return os.RemoveAll(filepath.Join(dir, entry.Name()))
	}); err != nil {
		return err
	}
	return syncDirectory(dir)
}
