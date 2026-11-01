package dbvm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/dbarchive"
	"github.com/onebox-faas/faas/pkg/dbguest"
)

// archiveDialTimeout bounds reaching dbbackupd; a failure fails the guest's
// archive_command, which PostgreSQL retries.
const archiveDialTimeout = 5 * time.Second

// archiveUDSPath is where Firecracker delivers guest-initiated vsock
// connections to the archive port: <uds>_<port>, inside the jail.
func (r *Runtime) archiveUDSPath(vm *databaseVM) string {
	return r.udsPath(vm) + "_" + strconv.FormatUint(uint64(dbguest.ArchivePort), 10)
}

// startArchiveRelay listens for the VM's archive streams and relays each to
// dbbackupd after a header naming the database this runtime booted, so a
// guest can never address another database's archive.
func (r *Runtime) startArchiveRelay(vm *databaseVM, id string, req BootRequest) error {
	if r.cfg.ArchiveSocket == "" {
		return nil
	}
	header := dbarchive.Header{DatabaseID: id, RestoreSource: req.RestoreSource, RestoreWindowSeconds: req.RestoreWindowSeconds,
		QuotaBytes: r.archiveQuota(vm.volume)}
	if err := header.Validate(); err != nil {
		return err
	}
	vm.header.Store(&header)
	path := r.archiveUDSPath(vm)
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return fmt.Errorf("dbvm: archive relay: %w", err)
	}
	// Firecracker runs as the jail's uid and connects to this socket.
	if err := os.Chown(path, vm.lease.UID, vm.lease.GID); err != nil {
		_ = ln.Close()
		return fmt.Errorf("dbvm: archive relay: %w", err)
	}
	vm.archive = ln
	// PostgreSQL archives and restores one file at a time and takes one
	// base backup at a time, so a guest needs only a few streams; more
	// would only spend vmmd's and dbbackupd's connections.
	slots := make(chan struct{}, archiveStreamsPerVM)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			select {
			case slots <- struct{}{}:
			default:
				_ = conn.Close()
				continue
			}
			// A spare's header changes when a database adopts it.
			go func() {
				defer func() { <-slots }()
				r.relayArchive(conn, *vm.header.Load())
			}()
		}
	}()
	return nil
}

// DropArchive deletes a deleted database's archive through dbbackupd's
// admin header; without archiving there is nothing to drop.
func (r *Runtime) DropArchive(ctx context.Context, id string) error {
	if err := validID(id); err != nil {
		return err
	}
	if r.cfg.ArchiveSocket == "" {
		return nil
	}
	if err := dbarchive.DropArchive(ctx, r.cfg.ArchiveSocket, id); err != nil {
		return fmt.Errorf("dbvm: drop archive: %w", err)
	}
	return nil
}

// PruneArchive prunes a database's archive in the bucket to its restore
// window, through dbbackupd's admin header.
func (r *Runtime) PruneArchive(ctx context.Context, id string, windowSeconds int64) (dbarchive.PruneResult, error) {
	if err := validID(id); err != nil {
		return dbarchive.PruneResult{}, err
	}
	if r.cfg.ArchiveSocket == "" {
		return dbarchive.PruneResult{}, ErrNoArchive
	}
	res, err := dbarchive.PruneArchive(ctx, r.cfg.ArchiveSocket, id, windowSeconds)
	if err != nil {
		return res, fmt.Errorf("dbvm: prune archive: %w", err)
	}
	return res, nil
}

// SyncArchive ships the database's archive files still on this node to
// the bucket, through dbbackupd, and reports how many it uploaded. A node
// without archiving has nothing to ship.
func (r *Runtime) SyncArchive(ctx context.Context, id string) (int64, error) {
	if err := validID(id); err != nil {
		return 0, err
	}
	if r.cfg.ArchiveSocket == "" {
		return 0, nil
	}
	n, err := dbarchive.SyncArchive(ctx, r.cfg.ArchiveSocket, id)
	if err != nil {
		return n, fmt.Errorf("dbvm: sync archive: %w", err)
	}
	return n, nil
}

// ReleaseArchive ships what this node holds of a moved database's archive
// to the bucket and drops its copy here.
func (r *Runtime) ReleaseArchive(ctx context.Context, id string) error {
	if err := validID(id); err != nil {
		return err
	}
	if r.cfg.ArchiveSocket == "" {
		return nil
	}
	if err := dbarchive.ReleaseArchive(ctx, r.cfg.ArchiveSocket, id); err != nil {
		return fmt.Errorf("dbvm: release archive: %w", err)
	}
	return nil
}

// ArchiveBytes sizes each database's archive on this node through
// dbbackupd. Databases it could not size are left out.
func (r *Runtime) ArchiveBytes(ctx context.Context, ids []string) (map[string]int64, error) {
	if r.cfg.ArchiveSocket == "" {
		return nil, ErrNoArchive
	}
	out := make(map[string]int64, len(ids))
	for _, id := range ids {
		if validID(id) != nil {
			continue
		}
		if n, err := dbarchive.ArchiveBytes(ctx, r.cfg.ArchiveSocket, id); err == nil {
			out[id] = n
		}
	}
	return out, nil
}

// PutArchiveHold pins point in a database's archive under name, through
// dbbackupd's admin header; placing it again returns it unchanged
// (dbarchive holds).
func (r *Runtime) PutArchiveHold(ctx context.Context, id, name string, point time.Time) (dbarchive.Hold, error) {
	if err := validID(id); err != nil {
		return dbarchive.Hold{}, err
	}
	if r.cfg.ArchiveSocket == "" {
		return dbarchive.Hold{}, ErrNoArchive
	}
	hold, err := dbarchive.PutArchiveHold(ctx, r.cfg.ArchiveSocket, id, name, point)
	if err != nil {
		return hold, fmt.Errorf("dbvm: hold archive: %w", err)
	}
	return hold, nil
}

// GetArchiveHold returns a hold in a database's archive, or an error
// wrapping dbarchive.ErrNotFound.
func (r *Runtime) GetArchiveHold(ctx context.Context, id, name string) (dbarchive.Hold, error) {
	if err := validID(id); err != nil {
		return dbarchive.Hold{}, err
	}
	if r.cfg.ArchiveSocket == "" {
		return dbarchive.Hold{}, ErrNoArchive
	}
	hold, err := dbarchive.GetArchiveHold(ctx, r.cfg.ArchiveSocket, id, name)
	if err != nil {
		return hold, fmt.Errorf("dbvm: read archive hold: %w", err)
	}
	return hold, nil
}

// DeleteArchiveHold removes a hold from a database's archive; a missing
// one is success.
func (r *Runtime) DeleteArchiveHold(ctx context.Context, id, name string) error {
	if err := validID(id); err != nil {
		return err
	}
	if r.cfg.ArchiveSocket == "" {
		return ErrNoArchive
	}
	if err := dbarchive.DeleteArchiveHold(ctx, r.cfg.ArchiveSocket, id, name); err != nil {
		return fmt.Errorf("dbvm: remove archive hold: %w", err)
	}
	return nil
}

// ErrNoArchive reports a runtime without dbbackupd.
var ErrNoArchive = errors.New("dbvm: archiving is off on this node")

func (r *Runtime) relayArchive(guest net.Conn, header dbarchive.Header) {
	defer func() { _ = guest.Close() }()
	host, err := net.DialTimeout("unix", r.cfg.ArchiveSocket, archiveDialTimeout)
	if err != nil {
		return
	}
	defer func() { _ = host.Close() }()
	if err := dbarchive.WriteLine(host, header); err != nil {
		return
	}
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(host, guest)
		if cw, ok := host.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
		close(done)
	}()
	_, _ = io.Copy(guest, host)
	<-done
}

// archiveStreamsPerVM caps one VM's concurrent archive streams.
const archiveStreamsPerVM = 4
