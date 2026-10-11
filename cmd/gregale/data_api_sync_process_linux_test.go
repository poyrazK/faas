//go:build linux

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDataAPISyncCancellationStopsCommandDescendants(t *testing.T) {
	path, config := dataAPISyncTestConfig(t)
	config.Migrate.Command[len(config.Migrate.Command)-2] = "wait-child"
	writeDataAPISyncTestConfig(t, path, config)
	prepared, err := loadDataAPISyncConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	_, _, restore := swapIO(t)
	defer restore()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runDataAPISyncCommand(ctx, prepared.Migrate) }()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	var pid int
	for pid == 0 {
		select {
		case <-deadline.C:
			t.Fatal("command did not start its child")
		case <-ticker.C:
			content, _ := os.ReadFile(filepath.Join(prepared.Migrate.Directory, "child.pid"))
			pid, _ = strconv.Atoi(string(content))
		}
	}
	// Cleanup only the child acknowledged by this test, even if the assertion fails.
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("command cancellation: %v", err)
		}
	case <-deadline.C:
		t.Fatal("cancelled command did not return")
	}
	// An orphan may remain briefly as a zombie until the host init reaps it;
	// its process has exited and cannot keep executing or retain open pipes.
	for {
		status, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		_, fields, _ := strings.Cut(string(status), ") ")
		if err == nil && strings.HasPrefix(fields, "Z ") {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("local command descendant survived cancellation: %s, %v", status, err)
		case <-ticker.C:
		}
	}
}
