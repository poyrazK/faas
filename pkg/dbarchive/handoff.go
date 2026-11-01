package dbarchive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// Handing a database's archive to the bucket (ADR-1027 moves). A database
// moving to another node keeps writing to the same bucket prefix from its
// new node; the old node first ships everything it still holds (OpSync),
// and once the move is done drops its copy (OpRelease), which it would
// otherwise keep pruning and sizing as if it ran the database.

// Handoff ships and releases one database's archive; *Uploader does.
type Handoff interface {
	SyncDatabase(ctx context.Context, databaseID string) (int, error)
	Release(ctx context.Context, databaseID string) error
}

var _ Handoff = (*Uploader)(nil)

// SyncDatabase uploads every file of the database's archive still waiting
// for the bucket, now, and reports how many it shipped. It stops at the
// first failure.
func (u *Uploader) SyncDatabase(ctx context.Context, databaseID string) (int, error) {
	if !ValidDatabaseID(databaseID) {
		return 0, fmt.Errorf("dbarchive: invalid database id %q", databaseID)
	}
	u.Store.maint.Lock()
	defer u.Store.maint.Unlock()
	return u.syncLocked(ctx, databaseID)
}

func (u *Uploader) syncLocked(ctx context.Context, databaseID string) (int, error) {
	shipped := 0
	for _, f := range u.files(databaseID) {
		if _, err := os.Stat(uploadedMarker(u.Store.dir, databaseID, f.name)); err == nil {
			continue
		}
		if err := u.upload(ctx, databaseID, f); err != nil {
			return shipped, err
		}
		shipped++
	}
	return shipped, nil
}

// Release ships what the database's archive still holds here, then
// removes this node's copy. The bucket keeps everything; the off-site
// mirror copies from the bucket whatever it had not uploaded from here.
func (u *Uploader) Release(ctx context.Context, databaseID string) error {
	if !ValidDatabaseID(databaseID) {
		return fmt.Errorf("dbarchive: invalid database id %q", databaseID)
	}
	u.Store.maint.Lock()
	defer u.Store.maint.Unlock()
	if _, err := u.syncLocked(ctx, databaseID); err != nil {
		return err
	}
	for _, f := range u.files(databaseID) {
		if _, err := os.Stat(offsiteMarker(u.Store.dir, databaseID, f.name)); err == nil {
			continue
		}
		rel := filepath.Base(filepath.Dir(f.path)) + "/" + f.name
		if err := u.Store.journalOffsite(offsiteOp{Op: offsiteCopy, Database: databaseID, Path: rel}); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(filepath.Join(u.Store.dir, databaseID)); err != nil {
		return err
	}
	u.Store.windowsMu.Lock()
	delete(u.Store.windows, databaseID)
	u.Store.windowsMu.Unlock()
	return nil
}

// SyncArchive asks dbbackupd at socketPath to ship a database's pending
// archive files to the bucket, returning how many it uploaded.
func SyncArchive(ctx context.Context, socketPath, databaseID string) (int64, error) {
	resp, err := admin(ctx, socketPath, databaseID, Request{Op: OpSync})
	return resp.Size, err
}

// ReleaseArchive asks dbbackupd at socketPath to ship and then drop its
// copy of a database's archive.
func ReleaseArchive(ctx context.Context, socketPath, databaseID string) error {
	_, err := admin(ctx, socketPath, databaseID, Request{Op: OpRelease})
	return err
}
