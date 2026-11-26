package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"

	"github.com/gofrs/flock"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

const (
	mcpTaskInboxCheckpointVersion  = 1
	mcpTaskInboxCheckpointMaxBytes = 256 << 10
)

type mcpTaskInboxCheckpointFile struct {
	Version        int      `json:"version"`
	EndpointSHA256 string   `json:"endpointSha256"`
	Status         string   `json:"status"`
	Observed       []string `json:"observed"`
}

type mcpTaskInboxCheckpoint struct {
	root     *os.Root
	lock     *flock.Flock
	name     string
	file     mcpTaskInboxCheckpointFile
	observed map[string]struct{}
}

func openMCPTaskInboxCheckpoint(path, endpoint, status string) (*mcpTaskInboxCheckpoint, error) {
	path = filepath.Clean(path)
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("open inbox checkpoint directory: %w", err)
	}
	lock, err := lockMCPJournal(path + ".lock")
	if err != nil {
		_ = root.Close()
		return nil, fmt.Errorf("lock inbox checkpoint: %w", err)
	}
	checkpoint := &mcpTaskInboxCheckpoint{
		root: root, lock: lock, name: filepath.Base(path),
		file: mcpTaskInboxCheckpointFile{
			Version: mcpTaskInboxCheckpointVersion, EndpointSHA256: toolpublication.Digest([]byte(endpoint)), Status: status,
		},
	}
	if err := checkpoint.load(); err != nil {
		checkpoint.close()
		return nil, err
	}
	return checkpoint, nil
}

func (checkpoint *mcpTaskInboxCheckpoint) close() {
	_ = checkpoint.root.Close()
	_ = checkpoint.lock.Close()
}

func (checkpoint *mcpTaskInboxCheckpoint) load() error {
	body, err := readCustomerOperationPrivateReceiptAt(checkpoint.root, checkpoint.name, mcpTaskInboxCheckpointMaxBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read inbox checkpoint: %w", err)
	}
	var file mcpTaskInboxCheckpointFile
	if decodeCustomerOperationReceipt(body, &file) != nil || file.Version != mcpTaskInboxCheckpointVersion ||
		!toolpublication.ValidID(file.EndpointSHA256) || file.Observed == nil ||
		len(file.Observed) > maxMCPTaskInboxWatchPages*mcphosting.MaxTaskInboxLimit {
		return errors.New("invalid inbox checkpoint")
	}
	if file.EndpointSHA256 != checkpoint.file.EndpointSHA256 || file.Status != checkpoint.file.Status {
		return errors.New("inbox checkpoint belongs to another endpoint or status filter. Choose another --checkpoint-file")
	}
	observed := make(map[string]struct{}, len(file.Observed))
	for _, digest := range file.Observed {
		if _, duplicate := observed[digest]; duplicate || !toolpublication.ValidID(digest) {
			return errors.New("invalid inbox checkpoint observation")
		}
		observed[digest] = struct{}{}
	}
	checkpoint.observed = observed
	return nil
}

// The credential affects only suppression of events from a fresh authorized
// scan. A renewed credential has a new key and sees all current entries.
func mcpTaskInboxWatchKey(client *mcphosting.Client, status string) []byte {
	key := hmac.New(sha256.New, []byte(client.Token))
	_, _ = key.Write([]byte("gregale/mcp-task-inbox-checkpoint/v1\x00" + client.Endpoint + "\x00" + status))
	return key.Sum(nil)
}

func (checkpoint *mcpTaskInboxCheckpoint) save(observed map[string]struct{}) error {
	file := checkpoint.file
	file.Observed = make([]string, 0, len(observed))
	for digest := range observed {
		file.Observed = append(file.Observed, digest)
	}
	slices.Sort(file.Observed)
	body, err := json.Marshal(file)
	if err != nil {
		return err
	}
	if err := checkpoint.write(body); err != nil {
		return fmt.Errorf("save inbox checkpoint: %w", err)
	}
	checkpoint.observed = observed
	return nil
}

func (checkpoint *mcpTaskInboxCheckpoint) write(body []byte) error {
	root, name := checkpoint.root, checkpoint.name
	temporary := ".gregale-inbox-checkpoint-" + uuid.NewString()
	file, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close(); _ = root.Remove(temporary) }()
	if _, err := file.Write(body); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	publish := root.Link // First publication must never replace a file.
	if checkpoint.observed != nil {
		info, err := root.Lstat(name)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return errors.New("inbox checkpoint destination must be a private regular file")
		}
		publish = root.Rename
	}
	if err := publish(temporary, name); err != nil || runtime.GOOS == "windows" {
		return err
	}
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}
