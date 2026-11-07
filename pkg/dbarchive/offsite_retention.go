package dbarchive

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// offsiteRetention rechecks the mirror's own pins at the destructive
// boundary, including deletes queued by a pruner from an older release.
type offsiteRetention struct {
	held   bool
	anchor BackupManifest
}

func (o *Offsite) retention(ctx context.Context, databaseID string) (offsiteRetention, error) {
	floor, held, err := remoteHoldFloor(ctx, o.Remote, o.Prefix, databaseID)
	plan := offsiteRetention{held: held}
	if err != nil || !held {
		return plan, err
	}
	backups, err := listRemoteBackups(ctx, o.Remote, BaseKey(o.Prefix, databaseID, ""), nil)
	if err != nil {
		return plan, err
	}
	plan.anchor, err = selectBackup(ctx, remoteHistoryReader{o.Remote, o.Prefix}, databaseID, floor, backups)
	if errors.Is(err, ErrNotFound) {
		return plan, nil // no proven anchor: preserve every recovery file
	}
	if len(plan.anchor.StartWAL) != 24 || !ValidWALName(plan.anchor.StartWAL) {
		plan.anchor = BackupManifest{}
	}
	return plan, err
}

func (p offsiteRetention) protects(path string) bool {
	if !p.held {
		return false
	}
	if p.anchor.ID == "" {
		return true
	}
	dir, name, _ := strings.Cut(path, "/")
	if dir == "base" {
		id, ext, ok := strings.Cut(name, ".")
		return !ok || !ValidBackupID(id) || (ext != "json" && ext != "tar.zst") || id >= p.anchor.ID
	}
	return !ValidWALName(name) || strings.HasSuffix(name, ".history") || name[8:24] >= p.anchor.StartWAL[8:24]
}

// remoteHistoryReader reads bounded backup history from this mirror
// alone: local or primary files cannot prove a mirror recovery anchor.
type remoteHistoryReader struct {
	remote Remote
	prefix string
}

func (r remoteHistoryReader) OpenWAL(ctx context.Context, databaseID, name string) (io.ReadCloser, int64, error) {
	if !ValidDatabaseID(databaseID) || !ValidWALName(name) || !strings.HasSuffix(name, ".backup") {
		return nil, 0, fmt.Errorf("dbarchive: invalid backup history %q/%q", databaseID, name)
	}
	var buf bytes.Buffer
	if err := r.remote.Get(ctx, WALKey(r.prefix, databaseID, name), &limitedBuffer{buf: &buf, left: maxManifestBytes}); err != nil {
		return nil, 0, err
	}
	return io.NopCloser(bytes.NewReader(buf.Bytes())), int64(buf.Len()), nil
}
