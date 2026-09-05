//go:build linux

package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// Exercise the real protocol handler after injected accept errors. A listener
// that exits on the first transient error cannot answer the queued request.
func TestResumeAcceptSurvivesTransientErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		errs []error
	}{
		{"interrupted", []error{unix.EINTR}},
		{"aborted", []error{unix.ECONNABORTED}},
		{"repeated", []error{unix.EINTR, fmt.Errorf("accept: %w", unix.EINTR), unix.ECONNABORTED}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "resume.sock")
			if err := unix.Bind(fd, &unix.SockaddrUnix{Name: path}); err != nil {
				_ = unix.Close(fd)
				t.Fatal(err)
			}
			if err := unix.Listen(fd, 1); err != nil {
				_ = unix.Close(fd)
				t.Fatal(err)
			}
			conn, err := net.Dial("unix", path)
			if err != nil {
				_ = unix.Close(fd)
				t.Fatal(err)
			}
			defer func() { _ = conn.Close() }()
			if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
				t.Fatal(err)
			}
			var header [8]byte
			// An invalid message type exercises dispatch and the ACK wire path
			// without changing host entropy or the test machine's wall clock.
			binary.BigEndian.PutUint32(header[:4], VsockResumeMsgType+1)
			if _, err := conn.Write(header[:]); err != nil {
				t.Fatal(err)
			}
			calls := 0
			accept := func(fd, flags int) (int, unix.Sockaddr, error) {
				calls++
				if calls <= len(tc.errs) {
					return -1, nil, tc.errs[calls-1]
				}
				if calls > len(tc.errs)+1 {
					return -1, nil, unix.EBADF
				}
				raw, addr, err := unix.Accept4(fd, flags)
				if err == nil {
					got, flagErr := unix.FcntlInt(uintptr(raw), unix.F_GETFD, 0)
					if flagErr != nil || got&unix.FD_CLOEXEC == 0 {
						t.Errorf("accepted socket must not reach runner exec: flags=%d err=%v", got, flagErr)
					}
				}
				return raw, addr, err
			}
			acceptResumeConnsWith(fd, slog.New(slog.NewTextHandler(io.Discard, nil)), accept)
			if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
				t.Errorf("terminal accept error left listener open: %v", err)
			}
			var ack [1]byte
			if _, err := io.ReadFull(conn, ack[:]); err != nil {
				t.Fatalf("listener did not serve request after transient errors: %v", err)
			}
			if ack[0] != VsockResumeAckMessageType {
				t.Fatalf("ack=%d, want message-type rejection", ack[0])
			}
			if calls != len(tc.errs)+2 {
				t.Fatalf("accept calls=%d, want transient retries, connection, then terminal error", calls)
			}
		})
	}
}
