package realtime

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MigrateCallbackOutbox moves pending and dead-lettered events from an older
// spool root into the configured root. The old writer must be stopped before
// calling it. A copied file is fsynced in the destination before its source
// is removed, so an interrupted migration can safely be retried.
func MigrateCallbackOutbox(from, to string) error {
	if filepath.Clean(from) == filepath.Clean(to) {
		return nil
	}
	if _, err := os.Stat(from); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("realtime: inspect legacy callback outbox: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(to, "dead"), 0o700); err != nil {
		return fmt.Errorf("realtime: create persistent callback outbox: %w", err)
	}
	for _, subdir := range []string{"", "dead"} {
		if err := migrateCallbackOutboxDir(filepath.Join(from, subdir), filepath.Join(to, subdir)); err != nil {
			return err
		}
	}
	return nil
}

func migrateCallbackOutboxDir(from, to string) error {
	entries, err := os.ReadDir(from)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("realtime: read legacy callback outbox %s: %w", from, err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".json") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		if !validCallbackOutboxID(id) || !entry.Type().IsRegular() {
			return fmt.Errorf("realtime: legacy callback outbox entry %q is not a regular event", name)
		}
		source := filepath.Join(from, name)
		target := filepath.Join(to, name)
		payload, err := os.ReadFile(source)
		if err != nil {
			return fmt.Errorf("realtime: read legacy callback event %q: %w", name, err)
		}
		info, err := os.Lstat(target)
		switch {
		case err == nil:
			if !info.Mode().IsRegular() {
				return fmt.Errorf("realtime: persistent callback event %q is not a regular file", name)
			}
			existing, readErr := os.ReadFile(target)
			if readErr != nil {
				return fmt.Errorf("realtime: read persistent callback event %q: %w", name, readErr)
			}
			if !bytes.Equal(existing, payload) {
				return fmt.Errorf("realtime: callback event %q conflicts with persistent spool", name)
			}
			if err := syncCallbackOutboxDir(to); err != nil {
				return fmt.Errorf("realtime: sync persistent callback event %q: %w", name, err)
			}
		case errors.Is(err, os.ErrNotExist):
			if err := writeCallbackOutboxFile(target, payload); err != nil {
				return fmt.Errorf("realtime: persist legacy callback event %q: %w", name, err)
			}
		default:
			return fmt.Errorf("realtime: inspect persistent callback event %q: %w", name, err)
		}
		if err := os.Remove(source); err != nil {
			return fmt.Errorf("realtime: retire legacy callback event %q: %w", name, err)
		}
	}
	return nil
}
