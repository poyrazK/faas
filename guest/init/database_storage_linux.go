//go:build linux

package main

import (
	"context"
	"errors"
	"os"
	"time"

	"golang.org/x/sys/unix"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

const (
	// storageGuardInterval is how often the guard reads the volume.
	storageGuardInterval = 10 * time.Second
	// databaseBallast holds back space on the volume, outside the cluster.
	databaseBallast = databaseMountPoint + "/ballast"
)

// storageGuard is the guard's view, under databaseGuest.mu.
type storageGuard struct {
	// active is set while the guard governs a ready primary; it applies
	// its mode again whenever it becomes active (a boot, a promotion), as
	// the server's setting may be stale (a resize, a standby's copy).
	active   bool
	readOnly bool
}

// guardStorage runs the storage guard until the guest stops.
func (g *databaseGuest) guardStorage() {
	tick := time.NewTicker(storageGuardInterval)
	defer tick.Stop()
	for {
		g.guardStorageOnce()
		select {
		case <-g.done:
			return
		case <-tick.C:
		}
	}
}

func (g *databaseGuest) guardStorageOnce() {
	g.mu.Lock()
	primary := g.state == dbguest.StateReady && !g.standby
	active, readOnly := g.storage.active, g.storage.readOnly
	if !primary {
		g.storage.active = false
	}
	g.mu.Unlock()
	if !primary {
		return
	}
	total, free, err := importVolume()
	if err != nil {
		g.log.Warn("database storage: read the volume", "err", err)
		return
	}
	free = g.balanceBallast(total, free)
	if !active {
		// Take over the mode the server's files set.
		ctx, cancel := context.WithTimeout(context.Background(), databaseAdminSQLTimeout)
		out, err := g.queryIn(ctx, "postgres", storageModeQuery)
		cancel()
		if err != nil {
			g.log.Warn("database storage: read the mode", "err", err)
			return
		}
		readOnly = out == "t"
	}
	next := nextStorageReadOnly(readOnly, free, total)
	if next != readOnly {
		ctx, cancel := context.WithTimeout(context.Background(), databaseAdminSQLTimeout)
		err := g.applyStorageMode(ctx, next)
		cancel()
		if err != nil {
			g.log.Warn("database storage: set the mode", "read_only", next, "err", err)
			return
		}
		g.log.Info("database storage: mode", "read_only", next, "free_bytes", free, "total_bytes", total)
	}
	g.mu.Lock()
	g.storage = storageGuard{active: true, readOnly: next}
	g.mu.Unlock()
}

func (g *databaseGuest) applyStorageMode(ctx context.Context, readOnly bool) error {
	for _, statement := range storageModeStatements(readOnly) {
		if _, err := g.queryIn(ctx, "postgres", statement); err != nil {
			return err
		}
	}
	return nil
}

// balanceBallast gives the ballast up when space is nearly gone and lays
// it again once the database could turn writable with it in place. It
// returns the free space after either.
func (g *databaseGuest) balanceBallast(total, free uint64) uint64 {
	size := storageBallastBytes(total)
	info, err := os.Stat(databaseBallast)
	present := err == nil && info.Size() >= int64(size) //nolint:gosec // bounded by the volume
	switch {
	case present && free < storageBallastReleaseBelow:
		if err := os.Remove(databaseBallast); err != nil {
			g.log.Warn("database storage: release the ballast", "err", err)
			return free
		}
		g.log.Warn("database storage: released the ballast", "free_bytes", free)
		return free + uint64(info.Size()) //nolint:gosec // a file size
	case !present && free >= storageWritableAbove(total)+size:
		if err := layBallast(size); err != nil {
			g.log.Warn("database storage: lay the ballast", "err", err)
			return free
		}
		return free - size
	}
	return free
}

// layBallast allocates the ballast file's blocks.
func layBallast(size uint64) error {
	f, err := os.OpenFile(databaseBallast, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	err = unix.Fallocate(int(f.Fd()), 0, 0, int64(size)) //nolint:gosec // bounded by storageBallastBytes
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return errors.Join(err, os.Remove(databaseBallast))
	}
	return nil
}

// storageStatus reports the volume; nil when it cannot be read.
func (g *databaseGuest) storageStatus() *dbguest.StorageStatus {
	total, free, err := importVolume()
	if err != nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return &dbguest.StorageStatus{TotalBytes: int64(total), FreeBytes: int64(free), //nolint:gosec // volume sizes
		ReadOnly: g.storage.active && g.storage.readOnly, Low: free < storageWritableAbove(total)}
}
