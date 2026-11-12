//go:build linux

package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/connreleaseproto"
	"golang.org/x/sys/unix"
)

// releaseConnectionsTimeout bounds the whole dump-and-destroy pass. vmmd waits
// a little longer and destroys the VM either way.
const releaseConnectionsTimeout = 400 * time.Millisecond

// handleReleaseConnectionsConn answers connreleaseproto.MessageType: reset
// every off-guest TCP connection, then reply OK and the count. A failure still
// replies OK with what was reset: the VM is about to be destroyed regardless.
func handleReleaseConnectionsConn(f *os.File, log *slog.Logger, lengthHeader []byte) {
	if binary.BigEndian.Uint32(lengthHeader) != 0 {
		_, _ = f.Write([]byte{VsockResumeAckBodyLength})
		return
	}
	n, err := releaseGuestConnections(time.Now().Add(releaseConnectionsTimeout))
	if err != nil {
		log.Warn("release connections before destroy", "reset", n, "err", err)
	}
	var reply [connreleaseproto.ReplyBytes]byte
	reply[0] = VsockResumeAckOK
	binary.BigEndian.PutUint32(reply[1:], uint32(n))
	_, _ = f.Write(reply[:])
}

// releaseGuestConnections resets every TCP connection in releaseStates whose
// peer is off the guest, for IPv4 and IPv6, and returns how many it reset.
func releaseGuestConnections(deadline time.Time) (int, error) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_SOCK_DIAG)
	if err != nil {
		return 0, fmt.Errorf("sock_diag socket: %w", err)
	}
	defer func() { _ = unix.Close(fd) }()
	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return 0, fmt.Errorf("sock_diag bind: %w", err)
	}
	timeout := unix.NsecToTimeval(time.Until(deadline).Nanoseconds())
	if timeout.Sec <= 0 && timeout.Usec <= 0 {
		return 0, errors.New("sock_diag: deadline passed")
	}
	_ = unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &timeout)
	_ = unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_SNDTIMEO, &timeout)

	var seq uint32
	reset := 0
	var errs []error
	for _, family := range []uint8{unix.AF_INET, unix.AF_INET6} {
		seq++
		sockets, err := dumpTCPSockets(fd, seq, family)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, s := range sockets {
			if !s.releasable() {
				continue
			}
			if time.Now().After(deadline) {
				return reset, errors.Join(append(errs, errors.New("sock_diag: deadline passed"))...)
			}
			seq++
			if err := destroyTCPSocket(fd, seq, s); err != nil {
				// A kernel without CONFIG_INET_DIAG_DESTROY refuses every one.
				if errors.Is(err, unix.EOPNOTSUPP) {
					return reset, errors.Join(append(errs, err)...)
				}
				// ENOENT: the socket closed between the dump and the destroy.
				if !errors.Is(err, unix.ENOENT) {
					errs = append(errs, err)
				}
				continue
			}
			reset++
		}
	}
	return reset, errors.Join(errs...)
}

func dumpTCPSockets(fd int, seq uint32, family uint8) ([]diagSocket, error) {
	req := encodeInetDiagReq(sockDiagByFamily, unix.NLM_F_REQUEST|unix.NLM_F_DUMP, seq, family, releaseStates, nil)
	if err := unix.Sendto(fd, req, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return nil, fmt.Errorf("sock_diag dump send: %w", err)
	}
	var out []diagSocket
	buf := make([]byte, 64<<10)
	for {
		n, _, err := unix.Recvfrom(fd, buf, 0)
		if err != nil {
			return out, fmt.Errorf("sock_diag dump recv: %w", err)
		}
		msgs, err := syscall.ParseNetlinkMessage(buf[:n])
		if err != nil {
			return out, fmt.Errorf("sock_diag dump parse: %w", err)
		}
		for _, m := range msgs {
			if m.Header.Seq != seq {
				continue
			}
			switch m.Header.Type {
			case unix.NLMSG_DONE:
				return out, nil
			case unix.NLMSG_ERROR:
				return out, netlinkError(m.Data)
			case sockDiagByFamily:
				s, err := parseInetDiagMsg(m.Data)
				if err != nil {
					return out, err
				}
				out = append(out, s)
			}
		}
	}
}

func destroyTCPSocket(fd int, seq uint32, s diagSocket) error {
	req := encodeInetDiagReq(sockDestroy, unix.NLM_F_REQUEST|unix.NLM_F_ACK, seq, s.family, ^uint32(0), &s.id)
	if err := unix.Sendto(fd, req, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return fmt.Errorf("sock_diag destroy send: %w", err)
	}
	buf := make([]byte, 4096)
	for {
		n, _, err := unix.Recvfrom(fd, buf, 0)
		if err != nil {
			return fmt.Errorf("sock_diag destroy recv: %w", err)
		}
		msgs, err := syscall.ParseNetlinkMessage(buf[:n])
		if err != nil {
			return fmt.Errorf("sock_diag destroy parse: %w", err)
		}
		for _, m := range msgs {
			if m.Header.Seq == seq && m.Header.Type == unix.NLMSG_ERROR {
				return netlinkError(m.Data)
			}
		}
	}
}

// netlinkError decodes an NLMSG_ERROR payload; a zero code is the ACK.
func netlinkError(data []byte) error {
	if len(data) < 4 {
		return errors.New("sock_diag: short netlink error")
	}
	if code := int32(binary.NativeEndian.Uint32(data[:4])); code != 0 {
		return unix.Errno(-code)
	}
	return nil
}
