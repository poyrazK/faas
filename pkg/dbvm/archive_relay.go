package dbvm

import (
	"context"
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
	header := dbarchive.Header{DatabaseID: id, RestoreSource: req.RestoreSource, RestoreWindowSeconds: req.RestoreWindowSeconds}
	if err := header.Validate(); err != nil {
		return err
	}
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
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go r.relayArchive(conn, header)
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
