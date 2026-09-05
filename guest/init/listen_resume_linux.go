//go:build linux

package main

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// Vsock port the guest-init resume listener binds. The host side
// (vmmd's JailerVMM.TriggerResumeHook) dials VsockResumePort via FC's
// vsock proxy against the per-slot guest CID. ADR-022.
//
// The wire shape (CONNECT-port + JSON resume message + 1-byte ack) is
// covered by pkg/fcvm/vmm_test.go on the host side and
// main_linux_listen_resume_test.go on the guest side. The constants here
// must mirror pkg/fcvm/vmm.go::resumeHookGuestPort and resumeHookMsgResume;
// guest/init does not import pkg/fcvm (one-way layering).
const (
	// VsockResumePort is the AF_VSOCK port the resume hook listens on inside
	// the guest. The host's TriggerResumeHook dials this port via FC's vsock
	// proxy (vmm.go::resumeHookGuestPort). Must match on both sides.
	VsockResumePort = 1024
	// VsockResumeMsgType is the wire-format discriminator the guest accepts.
	// Matches pkg/fcvm/vmm.go::resumeHookMsgResume.
	VsockResumeMsgType uint32 = 1
	// VsockResumeAckOK / AckNack are the single-byte ack values written back
	// after the resume hook returns. 0 = ok, anything else = nack.
	VsockResumeAckOK   = 0
	VsockResumeAckNack = 1
	// The remaining values keep the wire fail-closed while making a metal
	// failure diagnosable. Older hosts treat every non-zero value as a NACK;
	// current vmmd includes the value in its error.
	VsockResumeAckHeader        = 6
	VsockResumeAckMessageType   = 7
	VsockResumeAckBodyLength    = 8
	VsockResumeAckBodyRead      = 9
	VsockResumeAckJSON          = 10
	VsockResumeAckEntropyBase64 = 11
	VsockResumeAckEntropyLength = 12
	// VsockResumeMaxEntropyBytes is the upper bound on the entropy payload
	// the guest will accept. Mirrors pkg/fcvm/vmm.go::resumeHookEntropyBytes
	// (256); we keep the host's constant in sync via the V6 metal test. If
	// the host ever ships more than this, we treat it as a wire error and
	// nack — never inject untrusted bytes into the entropy pool.
	VsockResumeMaxEntropyBytes = 256
	// VsockResumeMaxBodyBytes mirrors vmmd's resumeHookMaxBodyBytes. The
	// envelope includes the base64 entropy plus host time and traceparent;
	// capping it at encoded entropy + 64 rejected valid packets.
	VsockResumeMaxBodyBytes = 8 * 1024
)

// VsockResumeBindCID is the CID the guest's listener binds on. We use
// VMADDR_CID_ANY (0xffffffff) so the listener accepts inbound on whatever
// CID Firecracker assigned this instance (slot-derived, see pkg/fcvm/
// GuestVsockCID). The Linux kernel reports the host's CID as
// VMADDR_CID_HOST=2, so binding on 2 or 3 would target the wrong end of
// the connection.
const VsockResumeBindCID = 0xffffffff

// listenResumeHook opens an AF_VSOCK socket bound to VsockResumePort on
// VsockResumeBindCID (VMADDR_CID_ANY) and spawns acceptResumeConns in the
// background. Called from boot() before the supervisor starts the app, so
// the post-restore dial from vmmd never races the listener coming up.
//
// Binding on VMADDR_CID_ANY accepts inbound on whatever CID Firecracker
// assigned this instance — the slot-derived guest_cid from
// pkg/fcvm/GuestVsockCID. The Linux kernel's VMADDR_CID_HOST is 2 (NOT 3);
// binding on 2 would target the host kernel's vsock namespace, not ours.
//
// The listener is multi-shot: each connection runs the resume hook once and
// closes. Production Restore dials exactly once; a re-dial re-runs the hook,
// which is harmless on a healthy guest and useful for operator debugging.
//
// Idempotency: acceptResumeConns retries interrupted and aborted accepts. A
// terminal error closes the listener and is logged. The boot() caller does not
// wait on this goroutine.
func listenResumeHook(log *slog.Logger) error {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("vsock socket: %w", err)
	}
	addr := &unix.SockaddrVM{CID: VsockResumeBindCID, Port: VsockResumePort}
	if err := unix.Bind(fd, addr); err != nil {
		_ = unix.Close(fd)
		return fmt.Errorf("vsock bind (cid=VMADDR_CID_ANY) %d: %w", VsockResumePort, err)
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
	acceptResumeConnsWith(fd, log, unix.Accept4)
}

// Own the listening descriptor for the lifetime of the accept loop. A terminal
// error must not leave a listening socket with no goroutine to service it.
func acceptResumeConnsWith(fd int, log *slog.Logger, accept func(int, int) (int, unix.Sockaddr, error)) {
	defer func() { _ = unix.Close(fd) }()
	for {
		raw, _, err := accept(fd, unix.SOCK_CLOEXEC)
		if errors.Is(err, unix.EINTR) || errors.Is(err, unix.ECONNABORTED) {
			continue
		}
		if err != nil {
			log.Warn("vsock accept ended", "err", err)
			return
		}
		f := os.NewFile(uintptr(raw), "vsock")
		go handleResumeConn(f, log)
	}
}

// handleResumeConn reads the resume request, runs RunResumeHook, and writes
// the ack. Closes the file on return regardless of error.
//
// Wire format (ADR-022): 4-byte big-endian msg type + 4-byte big-endian body
// length + JSON body {"hostTimeUnixNano": N} + 1-byte ack. The length prefix
// keeps the guest off EOF-watching — some AF_VSOCK proxies don't propagate
// CloseWrite promptly through to the guest side.
func handleResumeConn(f *os.File, log *slog.Logger) {
	defer func() { _ = f.Close() }()

	var hdr [8]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		log.Warn("vsock read header", "err", err)
		resumeDiag(fmt.Sprintf("resume: read header err=%v", err))
		return
	}
	msgType := binary.BigEndian.Uint32(hdr[:4])
	if msgType != VsockResumeMsgType {
		log.Warn("vsock unknown msg type", "type", msgType)
		resumeDiag(fmt.Sprintf("resume: unknown message type=%d", msgType))
		_, _ = f.Write([]byte{VsockResumeAckMessageType})
		return
	}
	bodyLen := binary.BigEndian.Uint32(hdr[4:8])
	// Guard against a malicious peer pinning the guest on a giant body.
	// The cap mirrors vmmd's 8 KiB resumeHookMaxBodyBytes and is deliberately
	// independent of the entropy cap because the JSON envelope carries host
	// time and traceparent in addition to base64 entropy.
	if bodyLen == 0 || bodyLen > uint32(VsockResumeMaxBodyBytes) {
		log.Warn("vsock resume body length out of range", "len", bodyLen, "max", VsockResumeMaxBodyBytes)
		resumeDiag(fmt.Sprintf("resume: body length out of range=%d max=%d", bodyLen, VsockResumeMaxBodyBytes))
		_, _ = f.Write([]byte{VsockResumeAckBodyLength})
		return
	}
	body := make([]byte, bodyLen)
	if _, err := io.ReadFull(f, body); err != nil {
		log.Warn("vsock read body", "err", err)
		resumeDiag(fmt.Sprintf("resume: read body len=%d err=%v", bodyLen, err))
		_, _ = f.Write([]byte{VsockResumeAckBodyRead})
		return
	}
	var req struct {
		HostTimeUnixNano int64  `json:"hostTimeUnixNano"`
		Entropy          string `json:"entropy"`     // base64; decode + ioctl(RNDADDENTROPY)
		Traceparent      string `json:"traceparent"` // W3C; empty is tolerated (issues PR-4)
	}
	if err := json.Unmarshal(body, &req); err != nil {
		log.Warn("vsock resume body parse", "err", err)
		resumeDiag(fmt.Sprintf("resume: body json err=%v", err))
		_, _ = f.Write([]byte{VsockResumeAckJSON})
		return
	}
	var entropy []byte
	if req.Entropy != "" {
		decoded, decErr := base64.StdEncoding.DecodeString(req.Entropy)
		if decErr != nil {
			log.Warn("vsock resume entropy base64", "err", decErr)
			resumeDiag(fmt.Sprintf("resume: entropy base64 err=%v", decErr))
			_, _ = f.Write([]byte{VsockResumeAckEntropyBase64})
			return
		}
		if len(decoded) == 0 || len(decoded) > VsockResumeMaxEntropyBytes {
			log.Warn("vsock resume entropy length out of range", "len", len(decoded), "max", VsockResumeMaxEntropyBytes)
			resumeDiag(fmt.Sprintf("resume: entropy length out of range=%d max=%d", len(decoded), VsockResumeMaxEntropyBytes))
			_, _ = f.Write([]byte{VsockResumeAckEntropyLength})
			return
		}
		entropy = decoded
	}

	if err := RunResumeHook(req.HostTimeUnixNano, entropy); err != nil {
		// Keep ACK=1 as the generic NACK for malformed/unknown failures, but
		// preserve the failing stage on the wire for vmmd diagnostics. Every
		// non-zero value remains a fail-closed NACK to older hosts.
		ack := byte(VsockResumeAckNack)
		switch {
		case strings.Contains(err.Error(), "resume: add entropy:"):
			ack = 2
		case strings.Contains(err.Error(), "resume: reseed entropy:"):
			ack = 3
		case strings.Contains(err.Error(), "resume: step clock:"):
			ack = 4
		case strings.Contains(err.Error(), "resume: write uuid marker:"):
			ack = 5
		}
		log.Error("vsock resume hook failed", "err", err, "ack", ack)
		_, _ = f.Write([]byte{ack})
		return
	}
	// Issue #555 PR-4: stash the W3C traceparent for the supervisor
	// to read at Start(). The package var is the only safe seam
	// between the vsock accept goroutine and the supervisor's main
	// goroutine — the resume hook doesn't return a value, and the
	// runner env can't be threaded back through the supervisor
	// without a refactor that breaks the test fixture.
	SetResumeTraceparent(req.Traceparent)
	_, _ = f.Write([]byte{VsockResumeAckOK})
}
