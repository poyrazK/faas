//go:build linux

package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"

	"golang.org/x/sys/unix"
)

// Vsock host CID + port the guest-init resume listener binds. The host side
// (vmmd's JailerVMM.TriggerResumeHook) dials HostVsockCID:VsockResumePort
// against the per-slot guest CID. ADR-022.
//
// HostVsockCID is duplicated here (and also in pkg/fcvm/config.go)
// because guest/init does not import pkg/fcvm — the layering is one-
// way: pkg/fcvm talks to the guest via the wire, never the other way
// around. Both sides MUST stay equal; the wire-level smoke test in
// pkg/fcvm/vmm_test.go covers the host side, and TestListenResumeHook
// in main_linux_listen_resume_test.go covers the guest-side shape.
const (
	// HostVsockCID is the well-known hypervisor CID for the AF_VSOCK
	// listener. Firecracker's convention is CID 3 (matches pkg/fcvm).
	HostVsockCID    = 3
	VsockResumePort = 1024
	// VsockResumeMsgType is the wire-format discriminator the guest accepts.
	// Matches pkg/fcvm/vmm.go::resumeHookMsgResume.
	VsockResumeMsgType uint32 = 1
	// VsockResumeAckOK / AckNack are the single-byte ack values written back
	// after the resume hook returns. 0 = ok, anything else = nack.
	VsockResumeAckOK   = 0
	VsockResumeAckNack = 1
)

// listenResumeHook opens an AF_VSOCK socket bound to HostVsockCID:
// VsockResumePort and spawns acceptResumeConns in the background. Called from
// boot() before the supervisor starts the app, so the post-restore dial from
// vmmd never races the listener coming up.
//
// The listener is multi-shot: each connection runs the resume hook once and
// closes. Production Restore dials exactly once; a re-dial re-runs the hook,
// which is harmless on a healthy guest and useful for operator debugging.
//
// Idempotency: acceptResumeConns loops forever; on a syscall error (typically
// the VM exiting) it logs at Debug and returns. The boot() caller does not
// wait on this goroutine.
func listenResumeHook(log *slog.Logger) error {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		return fmt.Errorf("vsock socket: %w", err)
	}
	addr := &unix.SockaddrVM{CID: HostVsockCID, Port: VsockResumePort}
	if err := unix.Bind(fd, addr); err != nil {
		_ = unix.Close(fd)
		return fmt.Errorf("vsock bind %d:%d: %w", HostVsockCID, VsockResumePort, err)
	}
	if err := unix.Listen(fd, 1); err != nil {
		_ = unix.Close(fd)
		return fmt.Errorf("vsock listen: %w", err)
	}
	go acceptResumeConns(fd, log)
	return nil
}

// acceptResumeConns accepts connections on fd and dispatches each to a
// goroutine running handleResumeConn. Sequential accepts; each handle runs in
// its own goroutine so a slow hook does not back up the listener.
func acceptResumeConns(fd int, log *slog.Logger) {
	for {
		raw, _, err := unix.Accept(fd)
		if err != nil {
			log.Debug("vsock accept ended", "err", err)
			return
		}
		f := os.NewFile(uintptr(raw), "vsock")
		go handleResumeConn(f, log)
	}
}

// handleResumeConn reads the resume request, runs RunResumeHook, and writes
// the ack. Closes the file on return regardless of error.
//
// Wire format (ADR-022): 4-byte big-endian msg type + JSON body
// {"hostTimeUnixNano": N} + 1-byte ack.
func handleResumeConn(f *os.File, log *slog.Logger) {
	defer func() { _ = f.Close() }()

	var hdr [4]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		log.Warn("vsock read header", "err", err)
		return
	}
	msgType := binary.BigEndian.Uint32(hdr[:])
	if msgType != VsockResumeMsgType {
		log.Warn("vsock unknown msg type", "type", msgType)
		_, _ = f.Write([]byte{VsockResumeAckNack})
		return
	}

	// Body size: read until EOF (the host writes one short JSON blob then
	// closes its write side). A bounded read guards against a malicious peer
	// pinning the guest on a giant body.
	const maxBody = 1024
	body := make([]byte, 0, 64)
	buf := make([]byte, 256)
	for {
		if len(body) >= maxBody {
			log.Warn("vsock resume body too large", "max", maxBody)
			_, _ = f.Write([]byte{VsockResumeAckNack})
			return
		}
		n, err := f.Read(buf)
		if n > 0 {
			body = append(body, buf[:n]...)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Warn("vsock read body", "err", err)
			_, _ = f.Write([]byte{VsockResumeAckNack})
			return
		}
	}
	body = bytes.TrimRight(body, "\x00")
	var req struct {
		HostTimeUnixNano int64 `json:"hostTimeUnixNano"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		log.Warn("vsock resume body parse", "err", err)
		_, _ = f.Write([]byte{VsockResumeAckNack})
		return
	}

	if err := RunResumeHook(req.HostTimeUnixNano); err != nil {
		log.Error("vsock resume hook failed", "err", err)
		_, _ = f.Write([]byte{VsockResumeAckNack})
		return
	}
	_, _ = f.Write([]byte{VsockResumeAckOK})
}
