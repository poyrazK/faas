//go:build linux

package main

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/frameworkready"
	"golang.org/x/sys/unix"
)

const FrameworkReadyProxyPath = "/run/guest-init/framework-ready.sock"
const FrameworkReadyProxyDir = "/run/guest-init"
const FrameworkReadyProxyMode = 0o660

// Port 1027 is a guest STREAM listener reached through Firecracker's Unix socket.
const VsockFrameworkReadyPort uint32 = frameworkready.Port

// Store the first actual runner signal in guest memory. A warm snapshot keeps
// this state; each restored instance can report it without rerunning customer code.
type frameworkReadyState struct{ value atomic.Uint64 }

func (s *frameworkReadyState) mark(ms uint32) { s.value.CompareAndSwap(0, uint64(ms)<<1|1) }
func (s *frameworkReadyState) status() frameworkready.Status {
	v := s.value.Load()
	return frameworkready.Status{Ready: v&1 != 0, WarmupMs: uint32(v >> 1)}
}

func startFrameworkReadyProxy(log *slog.Logger, uid int) error {
	if log == nil {
		log = slog.Default()
	}
	if err := os.MkdirAll(FrameworkReadyProxyDir, 0o755); err != nil {
		return fmt.Errorf("proxy mkdir: %w", err)
	}
	if err := os.Remove(FrameworkReadyProxyPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("proxy unlink: %w", err)
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: FrameworkReadyProxyPath, Net: "unix"})
	if err != nil {
		return fmt.Errorf("proxy listen: %w", err)
	}
	if err = os.Chown(FrameworkReadyProxyPath, 0, uid); err != nil {
		_ = ln.Close()
		return fmt.Errorf("framework-ready socket ownership: %w", err)
	}
	if err = os.Chmod(FrameworkReadyProxyPath, FrameworkReadyProxyMode); err != nil {
		_ = ln.Close()
		return err
	}
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		_ = ln.Close()
		return fmt.Errorf("framework-ready socket: %w", err)
	}
	if err = unix.Bind(fd, &unix.SockaddrVM{CID: unix.VMADDR_CID_ANY, Port: VsockFrameworkReadyPort}); err == nil {
		err = unix.Listen(fd, 8)
	}
	if err != nil {
		_ = unix.Close(fd)
		_ = ln.Close()
		return fmt.Errorf("framework-ready listen: %w", err)
	}
	state := &frameworkReadyState{}
	go func() {
		defer func() { _ = ln.Close() }()
		for {
			conn, err := ln.AcceptUnix()
			if err != nil {
				return
			}
			handleFrameworkReadyConn(conn, state)
		}
	}()
	go serveFrameworkReadyStatus(fd, state, log)
	log.Info("framework_ready stream proxy started", "vsock_port", VsockFrameworkReadyPort)
	return nil
}

func serveFrameworkReadyStatus(fd int, state *frameworkReadyState, log *slog.Logger) {
	defer func() { _ = unix.Close(fd) }()
	for {
		raw, _, err := unix.Accept4(fd, unix.SOCK_CLOEXEC)
		if err == unix.EINTR || err == unix.ECONNABORTED {
			continue
		}
		if err != nil {
			log.Debug("framework-ready accept ended", "err", err)
			return
		}
		_ = unix.SetsockoptTimeval(raw, unix.SOL_SOCKET, unix.SO_SNDTIMEO, &unix.Timeval{Usec: 250000})
		f := os.NewFile(uintptr(raw), "framework-ready")
		_ = frameworkready.Write(f, state.status())
		_ = f.Close()
	}
}

func handleFrameworkReadyConn(conn net.Conn, state *frameworkReadyState) {
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(250 * time.Millisecond)); err != nil {
		return
	}
	line, err := bufio.NewReader(io.LimitReader(conn, 256)).ReadString('\n')
	if err != nil {
		_, _ = io.WriteString(conn, "err read_line\n")
		return
	}
	_, ms, err := parseProxyLine(line)
	if err != nil {
		_, _ = io.WriteString(conn, "err parse\n")
		return
	}
	state.mark(uint32(ms))
	_, _ = io.WriteString(conn, "ok\n")
}

func parseProxyLine(line string) (string, uint64, error) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) < 1 || len(fields) > 2 {
		return "", 0, fmt.Errorf("format: '<runtime> [warmup_ms]'")
	}
	runtime := fields[0]
	var warmupMs uint64
	if len(fields) == 2 {
		// ParseUint with bitSize=32 does the upper-bound check
		// against math.MaxUint32 AND rejects negative inputs in
		// the same call — both directions go/integer-overflow
		// flags. The wire is a 4-byte BE uint32, so anything
		// outside [0, math.MaxUint32] is unrepresentable anyway.
		w, perr := strconv.ParseUint(fields[1], 10, 32)
		if perr != nil {
			return "", 0, fmt.Errorf("parse warmup_ms: %w", perr)
		}
		warmupMs = w
	}
	return runtime, warmupMs, nil
}
