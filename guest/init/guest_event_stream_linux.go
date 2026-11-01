//go:build linux

package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/sys/unix"
)

// errEventPublishUnconfirmed: the whole event reached vmmd but no verdict
// came back, so it may or may not have been accepted.
var errEventPublishUnconfirmed = errors.New("event publish verdict not received")

// sendGuestEventFrame opens one Firecracker guest-initiated STREAM per event.
// Firecracker forwards it to the instance-specific host listener at
// <vsock.sock>_1027. A fresh stream gives the host an EOF frame boundary and
// avoids sharing a failed connection across independent lifecycle events.
func sendGuestEventFrame(frame []byte, timeout time.Duration) error {
	if len(frame) == 0 || len(frame) > frameworkReadyMaxStreamFrame {
		return fmt.Errorf("guest event frame length %d outside 1..%d", len(frame), frameworkReadyMaxStreamFrame)
	}
	if timeout <= 0 {
		timeout = time.Second
	}
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("guest event stream socket: %w", err)
	}
	defer func() { _ = unix.Close(fd) }()
	tv := unix.NsecToTimeval(timeout.Nanoseconds())
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_SNDTIMEO, &tv); err != nil {
		return fmt.Errorf("guest event stream send timeout: %w", err)
	}
	if err := unix.Connect(fd, &unix.SockaddrVM{CID: unix.VMADDR_CID_HOST, Port: VsockFrameworkReadyPort}); err != nil {
		return fmt.Errorf("guest event stream connect: %w", err)
	}
	if err := writeAllFD(fd, frame); err != nil {
		return fmt.Errorf("guest event stream write: %w", err)
	}
	return nil
}

// sendGuestEventPublishForVerdict sends one event as a type=0x0B frame and
// waits for vmmd's verdict (ADR-20261031102000). Both directions carry a
// length prefix: Firecracker's vsock does not reliably pass a half-close
// through, so neither side waits for EOF.
func sendGuestEventPublishForVerdict(body []byte, timeout time.Duration) (api.EventPublishVerdict, error) {
	if len(body) == 0 || len(body) > eventPublishMaxBody {
		return api.EventPublishVerdict{}, fmt.Errorf("guest event publish length %d outside 1..%d", len(body), eventPublishMaxBody)
	}
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return api.EventPublishVerdict{}, fmt.Errorf("guest event publish socket: %w", err)
	}
	defer func() { _ = unix.Close(fd) }()
	tv := unix.NsecToTimeval(timeout.Nanoseconds())
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_SNDTIMEO, &tv); err != nil {
		return api.EventPublishVerdict{}, fmt.Errorf("guest event publish send timeout: %w", err)
	}
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv); err != nil {
		return api.EventPublishVerdict{}, fmt.Errorf("guest event publish receive timeout: %w", err)
	}
	if err := unix.Connect(fd, &unix.SockaddrVM{CID: unix.VMADDR_CID_HOST, Port: VsockFrameworkReadyPort}); err != nil {
		return api.EventPublishVerdict{}, fmt.Errorf("guest event publish connect: %w", err)
	}
	if err := writeAllFD(fd, encodeEventPublishVerdictFrame(body)); err != nil {
		return api.EventPublishVerdict{}, fmt.Errorf("guest event publish write: %w", err)
	}
	return readEventPublishVerdict(func(p []byte) error { return readFullFD(fd, p) })
}

// encodeEventPublishVerdictFrame is type=0x0B, a 4-byte big-endian length,
// then the body.
func encodeEventPublishVerdictFrame(body []byte) []byte {
	frame := make([]byte, 5+len(body))
	frame[0] = eventPublishVerdictType
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(body)))
	copy(frame[5:], body)
	return frame
}

// readEventPublishVerdict reads vmmd's 2-byte big-endian length and JSON
// verdict. Anything short of a well-formed verdict is unconfirmed.
func readEventPublishVerdict(readFull func([]byte) error) (api.EventPublishVerdict, error) {
	var size [2]byte
	if err := readFull(size[:]); err != nil {
		return api.EventPublishVerdict{}, fmt.Errorf("%w: %w", errEventPublishUnconfirmed, err)
	}
	n := int(binary.BigEndian.Uint16(size[:]))
	if n == 0 || n > api.EventPublishVerdictMaxBytes {
		return api.EventPublishVerdict{}, fmt.Errorf("%w: verdict length %d", errEventPublishUnconfirmed, n)
	}
	reply := make([]byte, n)
	if err := readFull(reply); err != nil {
		return api.EventPublishVerdict{}, fmt.Errorf("%w: %w", errEventPublishUnconfirmed, err)
	}
	var verdict api.EventPublishVerdict
	if err := json.Unmarshal(reply, &verdict); err != nil || verdict.Status < 200 || verdict.Status > 599 {
		return api.EventPublishVerdict{}, fmt.Errorf("%w: malformed verdict", errEventPublishUnconfirmed)
	}
	return verdict, nil
}

func writeAllFD(fd int, frame []byte) error {
	for len(frame) > 0 {
		n, err := unix.Write(fd, frame)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		frame = frame[n:]
	}
	return nil
}

func readFullFD(fd int, p []byte) error {
	for len(p) > 0 {
		n, err := unix.Read(fd, p)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrUnexpectedEOF
		}
		p = p[n:]
	}
	return nil
}

// Lifecycle telemetry remains small, but the in-guest event publish proxy
// carries a bounded JSON payload. Keep one transport cap shared by both so a
// sender cannot allocate an unbounded frame before it reaches vmmd.
const frameworkReadyMaxStreamFrame = (64 << 10) + 1
