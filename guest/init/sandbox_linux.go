//go:build linux

package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/onebox-faas/faas/pkg/sandboxproto"
)

// sandboxGuest is set by sandbox mode before it can be captured: its
// commands start only after a restore's reseed (ADR-20261027090013 §2).
var sandboxGuest atomic.Bool

// runSandboxGuest serves a sandbox session until vmmd stops the VM, and
// powers off if the listener fails.
func runSandboxGuest(log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	defer func() { _ = poweroffExecution() }()
	if err := bringUpLoopback(); err != nil {
		log.Warn("sandbox: loopback unavailable", "err", err)
	} else if err := startOutboundRelay(log); err != nil {
		log.Warn("sandbox: outbound relay unavailable", "err", err)
	}
	if err := prepareSandboxDirs(); err != nil {
		log.Error("sandbox: prepare workspace", "err", err)
		return err
	}
	ln, err := listenGuestVsockBacklog(sandboxproto.VsockPort, sandboxMaxOperations)
	if err != nil {
		log.Error("sandbox: listen", "err", err)
		return err
	}
	defer func() { _ = ln.Close() }()
	sandboxGuest.Store(true)
	if err := listenResumeHook(log); err != nil {
		log.Warn("sandbox: resume listener unavailable", "err", err)
	}
	runner := execRunner{uid: sandboxUID, openPTY: openSandboxPTY, setSize: setAppTaskPTYSize}
	if os.Getpid() == 1 {
		runner.reap = startSandboxReaper().wait
	}
	srv := newSandboxServer(sandboxWorkspace, sandboxHome, sandboxUID, runner, log)
	guestStage(sandboxReadyStage)
	ctx := context.Background()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return err
			}
			log.Warn("sandbox: accept", "err", err)
			continue
		}
		go srv.serve(ctx, conn)
	}
}

// prepareSandboxDirs makes the workspace and the user's home, both owned by
// the session's user.
func prepareSandboxDirs() error {
	for _, dir := range []string{sandboxWorkspace, sandboxHome} {
		//nolint:forbidigo // fixed guest directories guest-init owns, not customer paths
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		//nolint:forbidigo // fixed guest directories guest-init owns, not customer paths
		if err := os.Chown(dir, sandboxUID, sandboxUID); err != nil {
			return err
		}
	}
	return nil
}

// openSandboxPTY allocates a terminal owned by uid.
func openSandboxPTY(uid int) (*os.File, *os.File, error) {
	return openAppTaskPTY(&syscall.Credential{Uid: uint32(uid), Gid: uint32(uid)})
}

// sandboxReaper is PID 1's only wait4(-1) in a session: many commands run at
// once, so each would otherwise reap the others' exits. It hands each exit
// to the command that waits for its pid; orphans it adopted are reaped and
// dropped. An exit that arrives before its command waits is kept for it.
type sandboxReaper struct {
	mu      sync.Mutex
	waiters map[int]chan sandboxExit
	early   map[int]sandboxExit
	// wake tells a reaper with no children that a command started.
	wake chan struct{}
}

// maxEarlyExits bounds the exits kept for commands not yet waiting, which
// also holds orphans' exits until it is full.
const maxEarlyExits = 1024

func startSandboxReaper() *sandboxReaper {
	r := &sandboxReaper{waiters: map[int]chan sandboxExit{}, early: map[int]sandboxExit{}, wake: make(chan struct{}, 1)}
	go r.loop()
	return r
}

func (r *sandboxReaper) loop() {
	for {
		var status unix.WaitStatus
		pid, err := unix.Wait4(-1, &status, 0, nil)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if errors.Is(err, syscall.ECHILD) {
			// No child: sleep until a command starts, without polling.
			<-r.wake
			continue
		}
		if err != nil || pid <= 0 {
			continue
		}
		r.deliver(pid, exitFromStatus(syscall.WaitStatus(status)))
	}
}

func (r *sandboxReaper) deliver(pid int, exit sandboxExit) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ch, ok := r.waiters[pid]; ok {
		delete(r.waiters, pid)
		ch <- exit
		return
	}
	if len(r.early) >= maxEarlyExits {
		for old := range r.early {
			delete(r.early, old)
			break
		}
	}
	r.early[pid] = exit
}

// wait returns the channel pid's exit arrives on. A runner calls it right
// after starting pid, which also wakes a reaper that had no children.
func (r *sandboxReaper) wait(pid int) <-chan sandboxExit {
	select {
	case r.wake <- struct{}{}:
	default:
	}
	ch := make(chan sandboxExit, 1)
	r.mu.Lock()
	defer r.mu.Unlock()
	if exit, ok := r.early[pid]; ok {
		delete(r.early, pid)
		ch <- exit
		return ch
	}
	r.waiters[pid] = ch
	return ch
}
