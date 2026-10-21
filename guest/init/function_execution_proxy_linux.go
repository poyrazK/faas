//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/functionlifetime"
	"golang.org/x/sys/unix"
)

// Only guest-init assigns process generations. A workload cannot claim a
// different runner generation, PID or VM by writing a field in a request.
type functionExecutionProxy struct {
	mu    sync.Mutex
	pid   int
	owner uint64
	dial  functionlifetime.Dial
}

var functionExecutions atomic.Pointer[functionExecutionProxy] // boot wiring

func startFunctionExecutionProxy(uid int) (*functionExecutionProxy, error) {
	path := functionlifetime.SocketPath
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err = os.Chown(path, 0, uid); err == nil {
		err = os.Chmod(path, 0o660)
	}
	if err != nil {
		_ = ln.Close()
		return nil, err
	}
	p := &functionExecutionProxy{dial: func(ctx context.Context) (net.Conn, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return dialRuntimeConfigVsock(functionlifetime.HostPort, functionlifetime.RPCTimeout)
	}}
	go p.serve(ln)
	return p, nil
}

func (p *functionExecutionProxy) bind(pid int) error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.owner == ^uint64(0) || pid < 1 || p.pid != 0 {
		return errors.New("function lifetime: invalid runner generation")
	}
	p.owner++
	p.pid = pid
	return nil
}

func (p *functionExecutionProxy) retire(pid int) {
	if p == nil {
		return
	}
	p.mu.Lock()
	if p.pid != pid {
		p.mu.Unlock()
		return
	}
	owner := p.owner
	p.pid = 0
	p.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), functionlifetime.RPCTimeout)
	defer cancel()
	// Parent exit does not prove interpreter children stopped. Retire seals
	// admission; vmmd kills the VM if executions remain owned.
	reply, err := functionlifetime.Call(ctx, p.dial, functionlifetime.Request{Version: functionlifetime.Version, Kind: functionlifetime.Retire, Owner: owner})
	if err != nil || reply.Status != functionlifetime.OK {
		slog.Warn("function execution owner retirement unconfirmed")
	}
}

func (p *functionExecutionProxy) serve(ln *net.UnixListener) {
	defer func() { _ = ln.Close() }()
	slots := make(chan struct{}, 64)
	for {
		conn, err := ln.AcceptUnix()
		if err != nil {
			return
		}
		select {
		case slots <- struct{}{}:
			go func() { defer func() { <-slots; _ = conn.Close() }(); p.handle(conn) }()
		default:
			_ = conn.Close()
		}
	}
}

func (p *functionExecutionProxy) handle(conn *net.UnixConn) {
	if err := conn.SetDeadline(time.Now().Add(functionlifetime.RPCTimeout)); err != nil {
		return
	}
	pid, err := functionExecutionPeerPID(conn)
	if err != nil {
		return
	}
	req, err := functionlifetime.ReadRequest(conn)
	if err != nil {
		return
	}
	p.mu.Lock()
	owner := p.owner
	valid := pid == p.pid && pid > 0
	p.mu.Unlock()
	if !valid || req.Owner != 0 || req.Kind == functionlifetime.Retire {
		_ = functionlifetime.Write(conn, functionlifetime.Reply{Version: functionlifetime.Version, Status: functionlifetime.Stale})
		return
	}
	req.Owner = owner
	reply, err := functionlifetime.Call(context.Background(), p.dial, req)
	if err != nil {
		// "Sent to host" is not "registered". The runner retries or
		// tombstones the same sequence when its ownership is uncertain.
		reply = functionlifetime.Reply{Version: functionlifetime.Version, Status: functionlifetime.Unavailable}
	}
	_ = functionlifetime.Write(conn, reply)
}

func functionExecutionPeerPID(conn *net.UnixConn) (int, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, err
	}
	var cred *unix.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) { cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) }); err != nil {
		return 0, err
	}
	if credErr != nil {
		return 0, fmt.Errorf("function lifetime: peer credentials: %w", credErr)
	}
	return int(cred.Pid), nil
}
