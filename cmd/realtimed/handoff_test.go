//go:build unix

package main

// adr: 2026101801

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

const realtimedHelperEnv = "REALTIMED_HANDOFF_HELPER"

// TestRealtimedHelperProcess runs realtimed as one generation; a hand-off
// re-executes this test binary with the same environment.
func TestRealtimedHelperProcess(t *testing.T) {
	if os.Getenv(realtimedHelperEnv) == "" {
		t.Skip("helper process")
	}
	pidFile := filepath.Join(os.Getenv(realtimedHelperEnv), "pid-"+strconv.Itoa(os.Getpid()))
	_ = os.WriteFile(pidFile, nil, 0o600)
	main()
}

// TestRealtimedHandoffKeepsServing reloads realtimed under load: the
// management socket and health listener answer every request, the old
// generation exits, and the successor keeps serving.
func TestRealtimedHandoffKeepsServing(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns realtimed generations")
	}
	dir, err := os.MkdirTemp("/tmp", "rt-handoff-") // short enough for a unix socket path
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	healthAddr := l.Addr().String()
	_ = l.Close()
	socket := filepath.Join(dir, "realtimed.sock")

	cmd := exec.Command(os.Args[0], "-test.run=^TestRealtimedHelperProcess$")
	cmd.Env = append(os.Environ(),
		realtimedHelperEnv+"="+dir,
		"FAAS_REALTIME_SOCKET="+socket,
		"FAAS_REALTIME_CALLBACK_OUTBOX="+filepath.Join(dir, "outbox"),
		"FAAS_REALTIME_HEALTH_LISTEN="+healthAddr,
	)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	oldPID := cmd.Process.Pid
	t.Cleanup(func() {
		entries, _ := os.ReadDir(dir)
		var pids []int
		for _, e := range entries {
			if pid, err := strconv.Atoi(strings.TrimPrefix(e.Name(), "pid-")); err == nil {
				pids = append(pids, pid)
				_ = syscall.Kill(pid, syscall.SIGTERM)
			}
		}
		_ = cmd.Wait()
		deadline := time.Now().Add(15 * time.Second)
		for _, pid := range pids {
			for syscall.Kill(pid, 0) == nil && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
		}
	})

	health := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	management := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}}
	get := func(c *http.Client, url string, wantOK bool) error {
		resp, err := c.Get(url) //nolint:noctx // test client
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		if wantOK && resp.StatusCode != http.StatusOK {
			return fmt.Errorf("%s: status %d", url, resp.StatusCode)
		}
		if resp.StatusCode >= 500 {
			return fmt.Errorf("%s: status %d", url, resp.StatusCode)
		}
		return nil
	}
	deadline := time.Now().Add(30 * time.Second)
	for get(health, "http://"+healthAddr+"/readyz", true) != nil {
		if time.Now().After(deadline) {
			t.Fatal("realtimed never became ready")
		}
		time.Sleep(50 * time.Millisecond)
	}

	var (
		failures atomic.Int64
		firstErr atomic.Value
		stop     = make(chan struct{})
		wg       sync.WaitGroup
	)
	probe := func(c *http.Client, url string, wantOK bool) {
		for {
			select {
			case <-stop:
				return
			case <-time.After(20 * time.Millisecond):
			}
			if err := get(c, url, wantOK); err != nil {
				failures.Add(1)
				firstErr.CompareAndSwap(nil, err.Error())
			}
		}
	}
	wg.Go(func() { probe(health, "http://"+healthAddr+"/readyz", true) })
	wg.Go(func() { probe(management, "http://realtimed/v1/unknown", false) })
	time.Sleep(200 * time.Millisecond)

	if err := cmd.Process.Signal(syscall.SIGUSR2); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("old generation exited with %v", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("old generation still running after the hand-off")
	}
	time.Sleep(500 * time.Millisecond)
	close(stop)
	wg.Wait()
	if n := failures.Load(); n != 0 {
		t.Fatalf("%d requests failed across the hand-off; first: %v", n, firstErr.Load())
	}
	if err := get(health, "http://"+healthAddr+"/readyz", true); err != nil {
		t.Fatalf("successor not serving after the old generation exited: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	successors := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "pid-") && e.Name() != "pid-"+strconv.Itoa(oldPID) {
			successors++
		}
	}
	if successors != 1 {
		t.Fatalf("%d successor generations started, want 1", successors)
	}
}
