package dbarchive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

// maxManifestBytes bounds a remote manifest read.
const maxManifestBytes = 64 << 10

// ListAllBackups returns the database's base backups on this node and in
// the bucket, oldest first. A bucket manifest counts only next to its data
// (uploads put data first, so this holds unless the data was deleted).
func (s *TieredStore) ListAllBackups(ctx context.Context, databaseID string) ([]BackupManifest, error) {
	local, err := s.ListBackups(databaseID)
	if err != nil || s.Remote == nil {
		return local, err
	}
	prefix := BaseKey(s.Prefix, databaseID, "")
	keys, err := s.Remote.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	present := make(map[string]bool, len(keys))
	for _, k := range keys {
		present[strings.TrimPrefix(k, prefix)] = true
	}
	known := make(map[string]bool, len(local))
	for _, m := range local {
		known[m.ID] = true
	}
	out := local
	for name := range present {
		id, ok := strings.CutSuffix(name, ".json")
		if !ok || !ValidBackupID(id) || known[id] || !present[id+".tar.zst"] {
			continue
		}
		var buf bytes.Buffer
		if err := s.Remote.Get(ctx, prefix+name, &limitedBuffer{buf: &buf, left: maxManifestBytes}); err != nil {
			return nil, err
		}
		var m BackupManifest
		if err := json.Unmarshal(buf.Bytes(), &m); err != nil || m.ID != id {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// SelectBackup picks from both tiers.
func (s *TieredStore) SelectBackup(ctx context.Context, wal Store, databaseID string, target time.Time) (BackupManifest, error) {
	backups, err := s.ListAllBackups(ctx, databaseID)
	if err != nil {
		return BackupManifest{}, err
	}
	return selectBackup(ctx, wal, databaseID, target, backups)
}

// OpenBackup reads the local copy, or streams the bucket's through the
// decoder: a backup can be as large as the volume, so it is not staged.
func (s *TieredStore) OpenBackup(ctx context.Context, databaseID, id string) (io.ReadCloser, error) {
	rc, err := s.LocalStore.OpenBackup(ctx, databaseID, id)
	if !errors.Is(err, ErrNotFound) || s.Remote == nil {
		return rc, err
	}
	ctx, cancel := context.WithCancel(ctx)
	pr, pw := io.Pipe()
	go func() {
		_ = pw.CloseWithError(s.Remote.Get(ctx, BaseKey(s.Prefix, databaseID, id+".tar.zst"), pw))
	}()
	dec, err := zstd.NewReader(pr)
	if err != nil {
		cancel()
		_ = pr.Close()
		return nil, err
	}
	return &remoteBackup{dec: dec, pipe: pr, cancel: cancel}, nil
}

type remoteBackup struct {
	dec    *zstd.Decoder
	pipe   *io.PipeReader
	cancel context.CancelFunc
}

func (r *remoteBackup) Read(p []byte) (int, error) { return r.dec.Read(p) }

func (r *remoteBackup) Close() error {
	r.dec.Close()
	r.cancel()
	return r.pipe.Close()
}

// limitedBuffer refuses writes past its limit.
type limitedBuffer struct {
	buf  *bytes.Buffer
	left int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if len(p) > l.left {
		return 0, errors.New("dbarchive: manifest too large")
	}
	l.left -= len(p)
	return l.buf.Write(p)
}
