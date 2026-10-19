package objectnode

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// BackupInfo describes a complete offline backup. SHA256 checks detect damage;
// they do not authenticate an archive obtained from an untrusted party.
type BackupInfo struct {
	Version        int       `json:"version"`
	Engine         string    `json:"engine"`
	FormatVersion  int       `json:"format_version"`
	ID             string    `json:"id"`
	StartedAt      time.Time `json:"started_at"`
	CompletedAt    time.Time `json:"completed_at"`
	Entries        int64     `json:"entries"`
	Files          int64     `json:"files"`
	Bytes          int64     `json:"bytes"`
	ManifestBytes  int64     `json:"manifest_bytes"`
	ManifestSHA256 string    `json:"manifest_sha256"`
}

type backupEntry struct {
	Path      string `json:"path"`
	Directory bool   `json:"directory"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
}

// Backup requires the daemon to be stopped. It acquires the same exclusive lock,
// performs normal startup recovery, and hashes every surviving blob against its
// catalog while copying. The destination must not exist, even if empty.
func Backup(ctx context.Context, source, destination string) (BackupInfo, error) {
	return createBackup(ctx, source, destination, nil)
}

func createBackup(ctx context.Context, source, destination string, checkpoint func(string) error) (BackupInfo, error) {
	info := BackupInfo{Version: 1, Engine: "gregale-object-backup", FormatVersion: 3, ID: uuid.NewString(), StartedAt: time.Now().UTC()}
	source, err := backupDirectory(source)
	if err != nil {
		return BackupInfo{}, err
	}
	destination, err = backupDestination(destination)
	if err != nil {
		return BackupInfo{}, err
	}
	if !backupPathsDisjoint(source, destination) {
		return BackupInfo{}, ErrInvalid
	}
	s, err := openBackupSource(ctx, source)
	if err != nil {
		return BackupInfo{}, err
	}
	defer func() { _ = s.Close() }()
	info.FormatVersion, err = nodeFormatVersion(s.root)
	if err != nil {
		return BackupInfo{}, err
	}
	stage, err := os.MkdirTemp(filepath.Dir(destination), ".object-backup-")
	if err != nil {
		return BackupInfo{}, err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if err = os.Mkdir(filepath.Join(stage, "data"), 0o700); err != nil {
		return BackupInfo{}, err
	}
	if err = s.copyBackup(ctx, stage, &info, checkpoint); err != nil {
		return BackupInfo{}, fmt.Errorf("object backup: copy and verify: %w", err)
	}
	info.CompletedAt = time.Now().UTC()
	if _, err = writeJSON(filepath.Join(stage, "backup.json"), info); err != nil {
		return BackupInfo{}, err
	}
	// Read back the completed artifact, including the destination's actual blob
	// bytes. A successful source read alone does not verify the backup disk.
	if _, err = VerifyBackup(ctx, stage); err != nil {
		return BackupInfo{}, fmt.Errorf("object backup: verify copied backup: %w", err)
	}
	if err = syncBackupTree(ctx, stage); err != nil {
		return BackupInfo{}, err
	}
	if err = publishBackupDirectory(ctx, stage, destination, checkpoint); err != nil {
		return BackupInfo{}, err
	}
	return info, nil
}

func openBackupSource(ctx context.Context, source string) (*Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s, err := lockStore(source)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*Store, error) { _ = s.Close(); return nil, err }
	var count int
	if err = walkBackupTree(ctx, source, "", 0, func(_ string, _ os.FileInfo) error {
		count++
		if count > api.ObjectNodeBackupMaxEntries+3 { // Process lock and both local derived indexes are not exported.
			return ErrCapacity
		}
		return nil
	}); err != nil {
		return fail(err)
	}
	var format struct {
		Engine  string `json:"engine"`
		Version int    `json:"version"`
	}
	if readJSON(filepath.Join(source, "format.json"), &format) != nil || format.Engine != "gregale-object-node" || format.Version < 3 || format.Version > 5 {
		return fail(errors.New("object backup: source must be an initialized format-3, format-4 or format-5 primary directory"))
	}
	if err = s.initialize(); err != nil {
		return fail(fmt.Errorf("object backup: recover source: %w", err))
	}
	if err = s.validateBackupCatalog(ctx); err != nil {
		return fail(fmt.Errorf("object backup: validate source: %w", err))
	}
	return s, nil
}

func (s *Store) copyBackup(ctx context.Context, stage string, info *BackupInfo, checkpoint func(string) error) error {
	f, err := os.OpenFile(filepath.Join(stage, "manifest.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	w := bufio.NewWriterSize(io.MultiWriter(f, h), api.ObjectNodeBackupBufferBytes)
	buffer := make([]byte, api.ObjectNodeBackupBufferBytes)
	err = walkBackupTree(ctx, s.root, "", 0, func(path string, stat os.FileInfo) error {
		if path == ".lock" || (path == recoveryIndexName || path == catalogIndexName) && stat.Mode().IsRegular() {
			return nil
		}
		e := backupEntry{Path: path, Directory: stat.IsDir()}
		if !validBackupPath(path, e.Directory) || info.Entries >= api.ObjectNodeBackupMaxEntries {
			return ErrCorrupt
		}
		destination := filepath.Join(stage, "data", path)
		if e.Directory {
			if err := os.Mkdir(destination, 0o700); err != nil {
				return err
			}
		} else {
			e.Size = stat.Size()
			e.SHA256, err = copyBackupFile(ctx, filepath.Join(s.root, path), destination, e.Size, buffer)
			if err != nil {
				return err
			}
			if err = s.verifyBackupDigest(path, e.Size, e.SHA256); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			info.Files++
			if err = addInventoryBytes(&info.Bytes, e.Size); err != nil {
				return err
			}
			if err = backupCheckpoint(ctx, checkpoint, "file"); err != nil {
				return err
			}
		}
		line, err := json.Marshal(e)
		if err != nil || len(line)+1 > api.ObjectNodeBackupRecordMaxBytes || int64(len(line)+1) > api.ObjectNodeBackupManifestMaxBytes-info.ManifestBytes {
			return ErrCapacity
		}
		// Include buffered manifest bytes and the small completion record in
		// the free-space check before a buffer flush can consume them.
		if err = backupSpace(stage, int64(w.Buffered()+len(line)+1+api.ObjectNodeBackupRecordMaxBytes)); err != nil {
			return err
		}
		if _, err = w.Write(append(line, '\n')); err != nil {
			return err
		}
		info.ManifestBytes += int64(len(line) + 1)
		info.Entries++
		return nil
	})
	if err != nil {
		return err
	}
	if err = w.Flush(); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	info.ManifestSHA256 = hex.EncodeToString(h.Sum(nil))
	return f.Close()
}
