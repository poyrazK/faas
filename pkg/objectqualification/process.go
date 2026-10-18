//go:build linux

package objectqualification

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

type child struct {
	command *exec.Cmd
	done    chan struct{}
	err     error
}

func (l *lab) spawn(ctx context.Context, binary, label string, args ...string) (*child, error) {
	// #nosec G204 -- Explicit operator-supplied executable, fixed argv; no shell.
	command := exec.CommandContext(ctx, binary, args...)
	command.Env = append(withoutNodeCredentials(os.Environ()), "FAAS_OBJECT_NODE_ACCESS_KEY_ID="+l.key, "FAAS_OBJECT_NODE_SECRET_ACCESS_KEY="+l.secret)
	if label == "primary" {
		command.Env = append(command.Env, objectstorage.OwnedNodeEnabledEnv+"=1", objectstorage.OwnedNodeServerEnabledEnv+"=1")
	}
	log, err := os.CreateTemp(l.root, label+"-*.log")
	if err != nil {
		return nil, err
	}
	command.Stdout, command.Stderr = io.Discard, log
	var progress *os.File
	if label == "follower" {
		progress, err = os.CreateTemp(l.root, "follower-progress-*.jsonl")
		if err != nil {
			_ = log.Close()
			return nil, err
		}
		command.Stdout = &replicationProgressWriter{output: progress}
	}
	return startManagedCommand(command, func() {
		_ = log.Close()
		if progress != nil {
			_ = progress.Close()
		}
	})
}

func startManagedCommand(command *exec.Cmd, finished func()) (*child, error) {
	command.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	c := &child{command: command, done: make(chan struct{})}
	started := make(chan error, 1)
	go func() {
		// Linux binds Pdeathsig to the creating thread. Keep that thread alive
		// until Wait reaps the child, including if the agent/controller dies.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		startErr := command.Start()
		started <- startErr
		if startErr == nil {
			c.err = command.Wait()
		}
		finished()
		close(c.done)
	}()
	if err := <-started; err != nil {
		return nil, err
	}
	return c, nil
}

func withoutNodeCredentials(environment []string) []string {
	result := make([]string, 0, len(environment))
	for _, item := range environment {
		name, _, _ := strings.Cut(item, "=")
		if name != "FAAS_OBJECT_NODE_ACCESS_KEY_ID" && name != "FAAS_OBJECT_NODE_SECRET_ACCESS_KEY" && !objectstorage.IsOwnedNodeFeatureEnv(name) {
			result = append(result, item)
		}
	}
	return result
}

func stopChild(c *child, killed bool) error {
	if c == nil {
		return nil
	}
	select {
	case <-c.done:
		return c.err
	default:
	}
	sig := os.Signal(syscall.SIGTERM)
	if killed {
		sig = os.Kill
	}
	if err := c.command.Process.Signal(sig); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	timer := time.NewTimer(api.ObjectQualificationStopTimeout)
	defer timer.Stop()
	select {
	case <-c.done:
		if killed {
			return nil
		}
		return c.err
	case <-timer.C:
		_ = c.command.Process.Kill()
		<-c.done
		return errors.New("qualification child did not stop before its deadline")
	}
}

func loopbackAddresses() (string, string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", "", err
	}
	defer func() { _ = listener.Close() }()
	metrics, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", "", err
	}
	defer func() { _ = metrics.Close() }()
	// Hold both reservations together so the kernel cannot select one port twice.
	return listener.Addr().String(), metrics.Addr().String(), nil
}

func (l *lab) startPrimary(ctx context.Context, floor int64) error {
	if l.remotePrimary.Load() {
		if _, err := l.remote.call(ctx, agentRequest{Operation: "start-primary", Floor: floor}); err != nil {
			return err
		}
		l.proxy.target.Store(l.remote.endpoint)
		l.metrics.Store(l.remote.metrics)
		l.primaryGeneration.Add(1)
		return l.waitHead(ctx)
	}
	address, metrics, err := loopbackAddresses()
	if err != nil {
		return err
	}
	l.primary, err = l.spawn(ctx, l.options.Daemon, "primary", "--data-dir", l.primaryDir, "--listen", address, "--metrics-listen", metrics, "--scrub=false", "--min-free-bytes", strconv.FormatInt(floor, 10), "--max-concurrent-writes", strconv.Itoa(l.options.Workers+1), "--compact="+strconv.FormatBool(l.options.AutomaticCompaction), "--compact-records", strconv.FormatInt(l.options.CompactionRecords, 10))
	if err != nil {
		return err
	}
	l.primaryPID.Store(int64(l.primary.command.Process.Pid))
	l.proxy.target.Store("http://" + address)
	l.metrics.Store("http://" + metrics + "/metrics")
	l.primaryGeneration.Add(1)
	return l.waitHead(ctx)
}

func (l *lab) waitHead(ctx context.Context) error {
	wait, cancel := context.WithTimeout(ctx, api.ObjectQualificationStartupTimeout)
	defer cancel()
	return poll(wait, func() (bool, error) {
		if !l.remotePrimary.Load() && childHealthy(l.primary) != nil {
			return false, fmt.Errorf("qualification daemon exited; inspect logs in %s", l.root)
		}
		_, err := l.source.Head(wait)
		return err == nil, nil
	})
}

func (l *lab) startFollower(ctx context.Context) error {
	if l.remote != nil && !l.remotePrimary.Load() {
		_, err := l.remote.call(ctx, agentRequest{Operation: "start-follower", Source: l.remote.source})
		return err
	}
	var err error
	l.follower, err = l.spawn(ctx, l.options.Control, "follower", l.followerArgs(l.proxy.server.URL)...)
	if err == nil {
		l.followerPID.Store(int64(l.follower.command.Process.Pid))
	}
	return err
}

func (l *lab) stopFollower(ctx context.Context, killed bool) error {
	if l.remote != nil && !l.remotePrimary.Load() {
		return l.remote.stop(ctx, "stop-follower", killed)
	}
	err := stopChild(l.follower, killed)
	l.follower = nil
	l.followerPID.Store(0)
	return err
}

func (l *lab) stopPrimary(ctx context.Context, killed bool) error {
	if l.remotePrimary.Load() {
		return l.remote.stop(ctx, "stop-primary", killed)
	}
	err := stopChild(l.primary, killed)
	l.primary = nil
	l.primaryPID.Store(0)
	return err
}

func (l *lab) caughtUp(ctx context.Context) error {
	wait, cancel := context.WithTimeout(ctx, api.ObjectQualificationRequestTimeout)
	defer cancel()
	return poll(wait, func() (bool, error) {
		if l.remote != nil && !l.remotePrimary.Load() {
			if _, err := l.remote.call(wait, agentRequest{Operation: "health-follower"}); err != nil {
				return false, err
			}
		} else if err := childHealthy(l.follower); err != nil {
			return false, err
		}
		head, err := l.source.Head(wait)
		if err != nil {
			return false, nil //nolint:nilerr // Retry transient restart/partition errors only until the polling context expires.
		}
		if len(head.Followers) != 1 {
			return false, errors.New("missing registered follower cursor")
		}
		ready := head.ReceivedSequence == head.AppliedSequence && head.Followers[0].Sequence == head.AppliedSequence
		if l.mode == objectnode.DurabilityReplica {
			ready = ready && head.Durability != nil && head.Durability.ConfirmedSequence == head.AppliedSequence
		}
		return ready, nil
	})
}

func poll(ctx context.Context, check func() (bool, error)) error {
	ticker := time.NewTicker(api.ObjectQualificationSampleInterval)
	defer ticker.Stop()
	for {
		ready, err := check()
		if err != nil || ready {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (l *lab) close(ctx context.Context) {
	// Cleanup gets a bounded drain even after the qualification is canceled.
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ObjectQualificationStopTimeout)
	defer cancel()
	_ = l.stopFollower(cleanup, true)
	_ = l.stopPrimary(cleanup, true)
	if l.remote != nil {
		l.remote.close(cleanup)
	}
	if l.gateway != nil {
		l.gateway.server.Close()
	}
	if l.proxy != nil {
		l.proxy.server.Close()
		l.proxy.transport.CloseIdleConnections()
	}
}

func pidOf(value *atomic.Int64) int { return int(value.Load()) }

func (l *lab) newPath(name string) string { return filepath.Join(l.root, name) }

func (l *lab) followerArgs(source string) []string {
	return []string{"replica-catch-up", "--data-dir", l.followerDir, "--source", source, "--follow", "--work-bytes-per-second", strconv.FormatInt(l.options.WorkBytesPerSecond, 10), "--work-entries-per-second", strconv.Itoa(l.options.WorkEntriesPerSecond), "--compact=" + strconv.FormatBool(l.options.AutomaticCompaction), "--compact-records", strconv.FormatInt(l.options.CompactionRecords, 10)}
}

func (l *lab) catchUpOptions() objectnode.CatchUpOptions {
	return objectnode.CatchUpOptions{MaxEntries: api.ObjectNodeReplicationBatch, MaxBytes: api.ObjectNodeReplicationStepBytes, WorkBytesPerSecond: l.options.WorkBytesPerSecond, WorkEntriesPerSecond: l.options.WorkEntriesPerSecond}
}
