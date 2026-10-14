package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"

	"github.com/onebox-faas/faas/pkg/dbarchive"
	"github.com/onebox-faas/faas/pkg/dbguest"
)

// databasePromoteWait bounds how long a restore may recover before the
// guest reports it failed.
const databasePromoteWait = 2 * time.Hour

// restoreAndStart turns an empty volume into the restore source's cluster
// as of g.restoreTarget: it pulls the newest base backup consistent before
// the target, extracts it, and starts PostgreSQL in targeted recovery,
// which promotes at the target. The database reports ready only once
// promoted (ADR-1027 durability).
//
// A boot interrupted before the extraction finished finds no restore marker
// and starts over from an empty data directory; one interrupted later
// resumes recovery, which PostgreSQL restarts from its last restartpoint.
func (g *databaseGuest) restoreAndStart() {
	if err := g.fetchBaseBackup(); err != nil {
		g.fail(dbguest.ErrCodeRestoreFailed, "fetch base backup", err)
		return
	}
	signal := filepath.Join(databasePGData, "recovery.signal")
	if err := os.WriteFile(signal, nil, 0o600); err != nil {
		g.fail(dbguest.ErrCodeRestoreFailed, "write recovery.signal", err)
		return
	}
	if err := os.Chown(signal, g.uid, g.gid); err != nil {
		g.fail(dbguest.ErrCodeRestoreFailed, "own recovery.signal", err)
		return
	}
	if err := markDurable(databaseRestoreMarker); err != nil {
		g.fail(dbguest.ErrCodeRestoreFailed, "mark restore extracted", err)
		return
	}
	g.startPostgres()
}

// markDurable makes the volume's contents durable, then records that in
// the marker file at path.
func markDurable(path string) error {
	unix.Sync()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	err = f.Sync()
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	unix.Sync()
	return nil
}

func (g *databaseGuest) fetchBaseBackup() error {
	if err := os.RemoveAll(databasePGData); err != nil {
		return err
	}
	for _, dir := range []string{databaseMountPoint, databasePGData} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
		if err := os.Chown(dir, g.uid, g.gid); err != nil {
			return err
		}
	}
	conn, err := dialHostArchive()
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	pr, pw := io.Pipe()
	extracted := make(chan error, 1)
	go func() {
		err := extractBaseTar(pr, databasePGData, g.uid, g.gid)
		_, _ = io.Copy(io.Discard, pr)
		extracted <- err
	}()
	id, err := dbarchive.NewClient(conn).GetBackup(g.restoreTarget, pw)
	_ = pw.CloseWithError(err)
	if extractErr := <-extracted; err == nil {
		err = extractErr
	}
	if err != nil {
		return err
	}
	g.log.Info("database restore: base backup extracted", "backup", id, "target", g.restoreTarget)
	return nil
}

// awaitPromotion waits until a recovering server has promoted.
func (g *databaseGuest) awaitPromotion(exited chan struct{}) bool {
	ctx, cancel := context.WithTimeout(context.Background(), databasePromoteWait)
	defer cancel()
	for {
		inRecovery, err := g.query(ctx, "SELECT pg_is_in_recovery()")
		if err == nil && inRecovery == "f" {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-exited:
			return false
		case <-time.After(databaseReadyPoll):
		}
	}
}

// noteRecoveredTo records the last commit the restore replayed, so the
// host learns how far it got (before the target if the archive ended).
func (g *databaseGuest) noteRecoveredTo() {
	ctx, cancel := context.WithTimeout(context.Background(), databaseAdminSQLTimeout)
	defer cancel()
	out, err := g.query(ctx, "SELECT coalesce(pg_last_xact_replay_timestamp()::text, '')")
	if err == nil {
		var at time.Time
		if at, err = parseReplayTimestamp(out); err == nil {
			g.mu.Lock()
			g.recoveredTo = at
			toEnd := g.targetDropped
			g.mu.Unlock()
			g.log.Info("database restore: promoted", "recovered_to", at, "target", g.restoreTarget, "to_end_of_archive", toEnd)
			return
		}
	}
	g.log.Warn("database restore: last replayed commit unknown", "err", err)
}
