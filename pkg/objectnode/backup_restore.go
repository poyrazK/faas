package objectnode

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/api"
)

// VerifyBackup reads every byte, validates the complete manifest, rejects extra
// paths, and checks the recovered catalog without modifying the backup.
func VerifyBackup(ctx context.Context, directory string) (BackupInfo, error) {
	return readBackup(ctx, directory, "", nil)
}

// RestoreBackup verifies while copying to a private sibling directory. Only a
// fully validated, recovered, fsynced node is published at a new destination.
// Multipart expiry uses wall-clock time; restoration does not extend its TTL.
func RestoreBackup(ctx context.Context, directory, destination string) (BackupInfo, error) {
	return restoreBackup(ctx, directory, destination, nil)
}

func restoreBackup(ctx context.Context, directory, destination string, checkpoint func(string) error) (BackupInfo, error) {
	directory, err := backupDirectory(directory)
	if err != nil {
		return BackupInfo{}, err
	}
	destination, err = backupDestination(destination)
	if err != nil {
		return BackupInfo{}, err
	}
	if !backupPathsDisjoint(directory, destination) {
		return BackupInfo{}, ErrInvalid
	}
	stage, err := os.MkdirTemp(filepath.Dir(destination), ".object-restore-")
	if err != nil {
		return BackupInfo{}, err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	info, err := readBackup(ctx, directory, stage, checkpoint)
	if err != nil {
		return BackupInfo{}, err
	}
	verified, err := readBackupData(ctx, directory, stage, "", nil)
	if err != nil {
		return BackupInfo{}, fmt.Errorf("object backup: verify restored copy: %w", err)
	}
	if verified != info {
		return BackupInfo{}, ErrCorrupt
	}
	var s *Store
	if info.FormatVersion >= 4 {
		s, err = prepareReplicaBootstrap(stage)
	} else {
		s, err = openBackupSource(ctx, stage)
	}
	if err != nil {
		return BackupInfo{}, fmt.Errorf("object backup: recover restored node: %w", err)
	}
	defer func() { _ = s.Close() }()
	if err = syncBackupTree(ctx, stage); err != nil {
		return BackupInfo{}, err
	}
	if err = publishBackupDirectory(ctx, stage, destination, checkpoint); err != nil {
		return BackupInfo{}, err
	}
	return info, nil
}

func readBackup(ctx context.Context, directory, destination string, checkpoint func(string) error) (BackupInfo, error) {
	return readBackupData(ctx, directory, "", destination, checkpoint)
}

func readBackupData(ctx context.Context, directory, copiedData, destination string, checkpoint func(string) error) (BackupInfo, error) {
	directory, err := backupDirectory(directory)
	if err != nil {
		return BackupInfo{}, err
	}
	info, err := readBackupInfo(directory)
	if err != nil {
		return BackupInfo{}, err
	}
	// Inspect entry types before any catalog reader can follow a nested path.
	var count int64
	if err = walkBackupTree(ctx, directory, "", 0, func(_ string, _ os.FileInfo) error {
		count++
		if count > info.Entries+3 {
			return ErrCorrupt
		}
		return nil
	}); err != nil {
		return BackupInfo{}, err
	}
	if count != info.Entries+3 { // data directory, manifest and completion record.
		return BackupInfo{}, ErrCorrupt
	}
	data, err := backupDirectory(filepath.Join(directory, "data"))
	if err != nil {
		return BackupInfo{}, err
	}
	if copiedData != "" {
		data, err = backupDirectory(copiedData)
		if err != nil {
			return BackupInfo{}, err
		}
		if err = walkBackupTree(ctx, data, "", 0, func(_ string, _ os.FileInfo) error { return nil }); err != nil {
			return BackupInfo{}, err
		}
	}
	actualFormat, formatErr := nodeFormatVersion(data)
	if formatErr != nil || actualFormat != info.FormatVersion {
		return BackupInfo{}, ErrCorrupt
	}
	s := &Store{root: data}
	if err = s.validateBackupCatalog(ctx); err != nil {
		return BackupInfo{}, err
	}
	f, err := openBackupFile(filepath.Join(directory, "manifest.jsonl"))
	if err != nil {
		return BackupInfo{}, err
	}
	defer func() { _ = f.Close() }()
	stat, err := f.Stat()
	if err != nil || stat.Size() != info.ManifestBytes {
		return BackupInfo{}, ErrCorrupt
	}
	h := sha256.New()
	r := bufio.NewReaderSize(io.TeeReader(io.LimitReader(contextReader{ctx, f}, info.ManifestBytes+1), h), api.ObjectNodeBackupRecordMaxBytes)
	seen := make(map[string]bool) // Bounded by the versioned entry/manifest limits.
	buffer := make([]byte, api.ObjectNodeBackupBufferBytes)
	var entries, files, bytes int64
	for {
		line, readErr := r.ReadSlice('\n')
		if err = ctx.Err(); err != nil {
			return BackupInfo{}, err
		}
		if errors.Is(readErr, io.EOF) && len(line) == 0 {
			break
		}
		if readErr != nil || entries >= info.Entries {
			return BackupInfo{}, ErrCorrupt
		}
		var e backupEntry
		if decodeBackupRecord(line[:len(line)-1], &e) != nil || !validBackupEntry(e, seen) {
			return BackupInfo{}, ErrCorrupt
		}
		if err = verifyBackupEntry(ctx, s, e, destination, buffer); err != nil {
			return BackupInfo{}, fmt.Errorf("object backup: verify %s: %w", e.Path, err)
		}
		seen[e.Path] = e.Directory
		entries++
		if !e.Directory {
			files++
			if err = addInventoryBytes(&bytes, e.Size); err != nil {
				return BackupInfo{}, err
			}
			if err = backupCheckpoint(ctx, checkpoint, "file"); err != nil {
				return BackupInfo{}, err
			}
		}
	}
	if entries != info.Entries || files != info.Files || bytes != info.Bytes || hex.EncodeToString(h.Sum(nil)) != info.ManifestSHA256 {
		return BackupInfo{}, ErrCorrupt
	}
	// Every manifest path was checked once. Equal counts plus a walk rejecting
	// any unlisted path prove there are no omitted files, directories or aliases.
	if err = walkBackupTree(ctx, data, "", 0, func(path string, _ os.FileInfo) error {
		if _, found := seen[path]; !found {
			return ErrCorrupt
		}
		return nil
	}); err != nil {
		return BackupInfo{}, err
	}
	return info, ctx.Err()
}

func validBackupEntry(e backupEntry, seen map[string]bool) bool {
	if _, duplicate := seen[e.Path]; duplicate || !validBackupPath(e.Path, e.Directory) || e.Size < 0 || e.Size > api.MaxObjectUploadBytes {
		return false
	}
	if parent := filepath.ToSlash(filepath.Dir(e.Path)); parent != "." && !seen[parent] {
		return false
	}
	if e.Directory {
		return e.Size == 0 && e.SHA256 == ""
	}
	return validScrubFindingID(e.SHA256)
}

func verifyBackupEntry(ctx context.Context, s *Store, e backupEntry, destination string, buffer []byte) error {
	path := filepath.Join(s.root, e.Path)
	stat, err := os.Lstat(path)
	if err != nil || stat.IsDir() != e.Directory {
		return ErrCorrupt
	}
	output := ""
	if destination != "" {
		output = filepath.Join(destination, e.Path)
	}
	if e.Directory {
		if output != "" {
			return os.Mkdir(output, 0o700)
		}
		return nil
	}
	digest, err := copyBackupFile(ctx, path, output, e.Size, buffer)
	if err != nil {
		return err
	}
	if digest != e.SHA256 {
		return ErrDigest
	}
	return s.verifyBackupDigest(e.Path, e.Size, digest)
}
