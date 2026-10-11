package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/gofrs/flock"
)

var errMCPJournalBusy = errors.New("MCP journal is owned by another command; wait for it to finish")

const mcpJournalOwnerEnv = "GREGALE_MCP_JOURNAL_OWNER"

// Lock files stay in place: removing them would allow locking a different inode.
func lockMCPJournal(path string) (*flock.Flock, error) {
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() || err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("invalid MCP journal lock")
	}
	lock := flock.New(path, flock.SetPermissions(0600))
	locked, err := lock.TryLock()
	if err != nil || !locked {
		_ = lock.Close()
		if err != nil {
			return nil, err
		}
		return nil, errMCPJournalBusy
	}
	return lock, nil
}

// An adapter holds the handoff lock for its entire operation. A new owner must
// acquire it before taking ownership, including after the old parent crashes.
func ownMCPJournal(path string, adapter bool) (func(), error) {
	handoff, err := lockMCPJournal(path + ".adapter.lock")
	if err != nil {
		return nil, err
	}
	if adapter {
		probe, probeErr := lockMCPJournal(path + ".owner.lock")
		if !errors.Is(probeErr, errMCPJournalBusy) {
			if probe != nil {
				_ = probe.Close()
			}
			_ = handoff.Close()
			return nil, errors.New("MCP journal adapter requires an active release owner")
		}
		f, readErr := openCustomerFile(path + ".owner.lock")
		var token []byte
		if readErr == nil {
			token = make([]byte, 64)
			n, e := f.Read(token)
			readErr = e
			token = token[:n]
			_ = f.Close()
		}
		expected := os.Getenv(mcpJournalOwnerEnv)
		if readErr != nil || len(expected) != 64 || string(token) != expected {
			_ = handoff.Close()
			return nil, errors.New("MCP journal adapter does not belong to the active command")
		}
		return func() { _ = handoff.Close() }, nil
	}
	owner, err := lockMCPJournal(path + ".owner.lock")
	if err != nil {
		_ = handoff.Close()
		return nil, err
	}
	var random [32]byte
	if _, err = rand.Read(random[:]); err == nil {
		var file *os.File
		file, err = os.OpenFile(path+".owner.lock", os.O_WRONLY|os.O_TRUNC, 0600)
		if err == nil {
			_, err = file.WriteString(hex.EncodeToString(random[:]))
			_ = file.Close()
		}
	}
	if err != nil {
		_ = owner.Close()
		_ = handoff.Close()
		return nil, err
	}
	old, existed := os.LookupEnv(mcpJournalOwnerEnv)
	if err = os.Setenv(mcpJournalOwnerEnv, hex.EncodeToString(random[:])); err != nil {
		_ = owner.Close()
		_ = handoff.Close()
		return nil, err
	}
	_ = handoff.Close()
	return func() {
		if existed {
			_ = os.Setenv(mcpJournalOwnerEnv, old)
		} else {
			_ = os.Unsetenv(mcpJournalOwnerEnv)
		}
		_ = owner.Close()
	}, nil
}

func saveMCPNativeState(path string, s *mcpNativeReleaseState) error {
	lock, err := lockMCPJournal(path + ".write.lock")
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	current, err := loadMCPNativeState(path, s.Fingerprint)
	if err != nil {
		return err
	}
	if current.journalExists != s.journalExists || current.Revision != s.Revision {
		return errors.New("MCP journal changed since it was read; reload before retrying")
	}
	if s.Revision == ^uint64(0) {
		return errors.New("MCP journal revision exhausted")
	}
	next := *s
	next.Revision++
	next.journalExists = true
	b, err := json.Marshal(next)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".mcp-release-*")
	if err != nil {
		return err
	}
	defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	if err == nil {
		err = f.Close()
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err == nil && runtime.GOOS != "windows" {
		var dir *os.File
		dir, err = os.OpenFile(filepath.Dir(path), os.O_RDONLY, 0)
		if err == nil {
			err = dir.Sync()
			_ = dir.Close()
		}
	}
	if err != nil {
		return fmt.Errorf("persist MCP journal (reload before retrying): %w", err)
	}
	*s = next
	return nil
}
