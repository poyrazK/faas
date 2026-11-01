package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"golang.org/x/sys/unix"

	"github.com/onebox-faas/faas/pkg/dbarchive"
	"github.com/onebox-faas/faas/pkg/dbguest"
)

// archiveWALTimeout bounds one archive or restore transfer; PostgreSQL
// retries a failed archive_command and treats a failed restore_command as
// a missing segment.
const archiveWALTimeout = 5 * time.Minute

// runArchiveWAL is PostgreSQL's archive_command and restore_command
// (ADR-1027):
//
//	archive-wal put <path> <name>   archive one WAL file
//	archive-wal get <name> <path>   fetch one for recovery
//
// It returns the process exit code: 0 on success, 1 otherwise. A missing
// segment on get is a normal non-zero exit; recovery ends its search there.
func runArchiveWAL(args []string) int {
	if len(args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: archive-wal put <path> <name> | get <name> <path>")
		return 1
	}
	conn, err := dialHostArchive()
	if err != nil {
		fmt.Fprintf(os.Stderr, "archive-wal: %v\n", err)
		return 1
	}
	defer func() { _ = conn.Close() }()
	client := dbarchive.NewClient(conn)
	switch args[0] {
	case "put":
		err = archivePut(client, args[1], args[2])
	case "get":
		err = archiveGet(client, args[1], args[2])
	default:
		err = fmt.Errorf("unknown op %q", args[0])
	}
	if err != nil {
		if !errors.Is(err, dbarchive.ErrNotFound) {
			fmt.Fprintf(os.Stderr, "archive-wal %s: %v\n", args[0], err)
		}
		return 1
	}
	return 0
}

// archiveWALFile archives one WAL file over a stream of its own, as
// archive_command does.
func archiveWALFile(path, name string) error {
	conn, err := dialHostArchive()
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	return archivePut(dbarchive.NewClient(conn), path, name)
}

func archivePut(client *dbarchive.Client, path, name string) error {
	f, err := os.Open(path) //nolint:gosec,forbidigo // PostgreSQL's own pg_wal path (%p)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	return client.PutWAL(name, f, info.Size())
}

// archiveGet writes to a temporary file and renames it into place, so
// recovery never reads a partly fetched segment.
func archiveGet(client *dbarchive.Client, name, path string) error {
	tmp := path + ".fetching"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) //nolint:gosec // PostgreSQL's own pg_wal path (%p)
	if err != nil {
		return err
	}
	err = client.GetWAL(name, f)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// dialHostArchive opens a vsock stream to the host's archive port. The
// socket is blocking with kernel send/receive timeouts (AF_VSOCK has no Go
// deadline support), so a stalled host fails the transfer instead of
// hanging PostgreSQL's archiver.
func dialHostArchive() (net.Conn, error) {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for _, opt := range []int{unix.SO_SNDTIMEO, unix.SO_RCVTIMEO} {
		if err := setSockTimeout(fd, opt, archiveWALTimeout); err != nil {
			_ = unix.Close(fd)
			return nil, err
		}
	}
	if err := unix.Connect(fd, &unix.SockaddrVM{CID: unix.VMADDR_CID_HOST, Port: dbguest.ArchivePort}); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("connect host archive: %w", err)
	}
	return &runtimeConfigVsockConn{
		File:   os.NewFile(uintptr(fd), "archive-vsock"),
		local:  runtimeConfigVsockAddr{unix.VMADDR_CID_ANY, 0},
		remote: runtimeConfigVsockAddr{unix.VMADDR_CID_HOST, dbguest.ArchivePort},
	}, nil
}
